// Package codemode implements "Code Mode": instead of exposing each tool as
// a separate function call the model invokes one at a time, Tool wraps a
// set of ai.Tool values into a single run_code tool. The model is shown an
// API doc of the available functions and writes a short program that calls
// them, and that program is executed in a sandbox the caller supplies.
//
// The SDK ships no runtime. Sandbox is an interface the caller implements
// against whatever they trust to run model-written code — a subprocess, a
// container, an embedded interpreter (e.g. goja, a WASM runtime) — and
// Env.CallTool is the only bridge back from that sandbox into the
// underlying ai.Tools. codemode itself never executes code: it renders the
// API doc, decodes the run_code arguments, dispatches CallTool invocations
// by name, and post-processes the Sandbox's Result for the model.
//
// Security note: the sandbox implementer owns isolation. Whatever
// guarantees the returned Tool appears to have — resource limits, network
// access, filesystem access, wall-clock limits — are exactly the
// guarantees the supplied Sandbox.Execute provides. codemode does not
// enforce sandboxing on its own.
//
// Security note: approvals. A tool wrapped in ai.RequireApproval is checked
// before every dispatch from sandboxed code — if ApprovalRequired reports
// true for the call's args, dispatch refuses it with an error instead of
// executing it. There is no suspension channel from inside a sandbox: the
// outer GenerateText/StreamText tool loop only suspends a batch pending
// approval when it can hand the caller a resumable PendingApprovals result,
// and there is no equivalent hand-back from a running sandboxed program. So
// approval-requiring tools can never execute through code mode, refused or
// not — decide approvals before handing tools to codemode (e.g. only wrap
// already-approved tools), or make an inline decision (ApproveToolCall-style)
// OUTSIDE code mode, before the call ever reaches the sandbox.
package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// Sandbox executes model-written code. The SDK ships no runtime — the
// caller implements this against their own sandbox (subprocess, container,
// embedded interpreter). Execute must honor ctx cancellation.
type Sandbox interface {
	Execute(ctx context.Context, code string, env Env) (*Result, error)
}

// Env is the binding surface a Sandbox exposes to running code.
type Env struct {
	// CallTool dispatches a tool invocation from sandboxed code to the
	// underlying ai.Tools by name. Args is the raw JSON argument object.
	CallTool func(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// Result is what a Sandbox returns to the model.
type Result struct {
	Output string // printed/returned output, sent back to the model verbatim
	Logs   []string
}

// Options configures the run_code Tool. A nil *Options is equivalent to
// the zero value, which applies the documented defaults.
type Options struct {
	// Language names the language in the generated tool description and
	// API docs. Purely prompting — the Sandbox defines what actually
	// runs. Default "javascript".
	Language string
	// MaxOutputBytes truncates Result.Output sent to the model. 0 means
	// the default of 16384.
	MaxOutputBytes int
}

const (
	defaultLanguage       = "javascript"
	defaultMaxOutputBytes = 16384
)

// codeArgs is the {"code": string} argument shape the run_code tool's
// schema describes and its Execute decodes.
type codeArgs struct {
	Code string `json:"code"`
}

// codeSchemaTemplate is the hand-written JSON Schema for the run_code
// tool, with "<language>" substituted into the code field's description.
const codeSchemaTemplate = `{"type":"object","properties":{"code":{"type":"string","description":"The %s code to execute."}},"required":["code"],"additionalProperties":false}`

// codeTool is the ai.Tool implementation Tool returns.
type codeTool struct {
	sandbox        Sandbox
	byName         map[string]ai.Tool
	description    string
	schema         json.RawMessage
	maxOutputBytes int
}

// Tool wraps tools into a single "run_code" ai.Tool: the model writes code
// that calls the tools through the sandbox's binding, instead of invoking
// them one call at a time.
//
// Tool panics if two entries in tools share the same Name(). Tool has no
// error return (the signature is fixed by the brief), so a duplicate name
// is treated as a programmer error rather than something to silently drop:
// without this check the dispatch map would resolve calls to whichever
// tool happened to be last in the slice while APIDoc would still document
// both entries, leaving one documented function the model can never
// actually reach. This mirrors ai.NewTool's construction-time panic on
// schema-derivation failure (itself likened to regexp.MustCompile).
func Tool(sandbox Sandbox, tools []ai.Tool, opts *Options) ai.Tool {
	language := defaultLanguage
	maxOutputBytes := defaultMaxOutputBytes
	if opts != nil {
		if opts.Language != "" {
			language = opts.Language
		}
		if opts.MaxOutputBytes != 0 {
			maxOutputBytes = opts.MaxOutputBytes
		}
	}

	byName := make(map[string]ai.Tool, len(tools))
	for _, t := range tools {
		if _, dup := byName[t.Name()]; dup {
			panic(fmt.Sprintf("codemode: duplicate tool name %q", t.Name()))
		}
		byName[t.Name()] = t
	}

	return &codeTool{
		sandbox:        sandbox,
		byName:         byName,
		description:    buildDescription(language, tools),
		schema:         json.RawMessage(fmt.Sprintf(codeSchemaTemplate, language)),
		maxOutputBytes: maxOutputBytes,
	}
}

// buildDescription assembles the run_code tool's description: a fixed
// preamble naming the language, the generated API doc for tools, and the
// usage rules the model needs to write valid calls.
func buildDescription(language string, tools []ai.Tool) string {
	preamble := fmt.Sprintf("Execute %s code in a sandbox. The following functions are available to your code:", language)
	usage := "Call functions with a single object argument matching the schema; return or print your final answer."
	return preamble + "\n\n" + APIDoc(language, tools) + "\n\n" + usage
}

// Name implements ai.Tool.
func (t *codeTool) Name() string { return "run_code" }

// Description implements ai.Tool.
func (t *codeTool) Description() string { return t.description }

// Schema implements ai.Tool.
func (t *codeTool) Schema() json.RawMessage { return t.schema }

// Execute implements ai.Tool. It decodes {"code": string}, runs it in the
// sandbox with a CallTool binding that dispatches to the wrapped tools by
// name, and returns the sandbox's Result rendered as a single string
// (Output, truncated to MaxOutputBytes, followed by any Logs). Sandbox
// errors are returned as-is (not wrapped) so the ai tool loop's usual
// *ai.ToolExecutionError wrapping applies exactly once.
func (t *codeTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	var a codeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}

	env := Env{CallTool: t.dispatch}
	result, err := t.sandbox.Execute(ctx, a.Code, env)
	if err != nil {
		return nil, err
	}

	output := result.Output
	if len(output) > t.maxOutputBytes {
		output = truncateOutput(output, t.maxOutputBytes) + "\n[truncated]"
	}
	var b strings.Builder
	b.WriteString(output)
	for _, line := range result.Logs {
		b.WriteString("\nlog: ")
		b.WriteString(line)
	}
	return b.String(), nil
}

// truncateOutput cuts s to at most n bytes, backing the cut point up to the
// start of the previous UTF-8 rune when a naive byte-slice at n would land
// in the middle of a multi-byte sequence. n is assumed < len(s).
func truncateOutput(s string, n int) string {
	cut := n
	// utf8.RuneStart(b) is true for a byte that begins a rune (or any
	// single-byte ASCII byte); walk backward at most utf8.UTFMax-1 bytes to
	// find one, matching how far into a multi-byte sequence n could land.
	for cut > 0 && cut > n-utf8.UTFMax && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// dispatch resolves name against the wrapped tools and executes it,
// passing ctx through unchanged so ai.RuntimeContextFrom works inside the
// dispatched tool. An unknown name is reported as a plain error (never a
// panic) listing the available tool names, sorted for determinism.
//
// Security: if the resolved tool implements ai.ApprovalRequirer and
// ApprovalRequired reports true for these args, the call is refused rather
// than executed. There is no suspension channel from inside a sandbox —
// code mode either runs a tool to completion or not at all, so a call that
// would otherwise suspend the outer GenerateText/StreamText loop pending a
// human decision must not be allowed to silently execute instead. Decide
// approvals before handing tools to codemode, or make an inline decision
// (ApproveToolCall-style) OUTSIDE code mode.
func (t *codeTool) dispatch(ctx context.Context, name string, args json.RawMessage) (any, error) {
	tool, ok := t.byName[name]
	if !ok {
		names := make([]string, 0, len(t.byName))
		for n := range t.byName {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("codemode: unknown tool %q; available tools: %s", name, strings.Join(names, ", "))
	}
	if ar, ok := tool.(ai.ApprovalRequirer); ok && ar.ApprovalRequired(ctx, args) {
		return nil, fmt.Errorf("codemode: tool %q requires approval and cannot be called from code mode", name)
	}
	return tool.Execute(ctx, args)
}
