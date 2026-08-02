// Package partialjson repairs a truncated (partial) JSON document — the kind
// produced mid-stream while accumulating a growing JSON string from an LLM —
// into a syntactically valid JSON document that best-effort preserves the
// content already present.
package partialjson

// state identifies what the scanner currently expects next.
type state int

const (
	stateValue      state = iota // expecting the start of a JSON value
	stateObjKey                  // expecting '"' to start an object key, or '}' to close
	stateObjColon                // expecting ':' after an object key
	stateAfterValue              // just finished a value; expecting ',' or a closer
	stateString                  // inside a JSON string (see isKey / escaping / unicodeLeft)
	stateNumber                  // inside a JSON number literal
	stateLiteral                 // inside true/false/null
)

// frameKind distinguishes object vs array containers on the open stack.
type frameKind int

const (
	frameObject frameKind = iota
	frameArray
)

type frame struct {
	kind       frameKind
	entryStart int // out length marking the start of the current (possibly incomplete) entry
}

// Repair completes a truncated JSON document so it parses: closes open
// strings, arrays, and objects; trims a trailing comma, colon-without-value,
// or partial literal (tru, fal, nul, bare '-'). Returns ok=false when no
// valid completion exists (e.g. empty input or input not starting a value).
func Repair(s string) (repaired string, ok bool) {
	var out []byte
	var stack []frame

	st := stateValue

	// String-scanning sub-state.
	isKey := false
	escaping := false
	unicodeLeft := 0
	escStart := 0 // out length at the start of the current \ escape sequence

	// Number-scanning sub-state.
	numStart := 0

	// Literal-scanning sub-state.
	var litTarget string
	litMatched := 0

	n := len(s)
	i := 0
	done := false

	// completeValue is called whenever a value (of any kind) finishes while
	// there is still input left to process. If the container stack is now
	// empty, the top-level value is complete and any trailing input is
	// ignored.
	completeValue := func() {
		if len(stack) == 0 {
			done = true
			return
		}
		st = stateAfterValue
	}

skipAndDispatch:
	for !done {
		// States that may be preceded by insignificant whitespace.
		if st == stateValue || st == stateObjKey || st == stateObjColon || st == stateAfterValue {
			for i < n && isSpace(s[i]) {
				i++
			}
		}

		if i >= n {
			break
		}

		c := s[i]

		switch st {
		case stateValue:
			switch {
			case c == '"':
				out = append(out, c)
				i++
				isKey = false
				st = stateString
			case c == '{':
				out = append(out, c)
				i++
				stack = append(stack, frame{kind: frameObject, entryStart: len(out)})
				st = stateObjKey
			case c == '[':
				out = append(out, c)
				i++
				stack = append(stack, frame{kind: frameArray, entryStart: len(out)})
				st = stateValue
			case c == ']' && len(stack) > 0 && stack[len(stack)-1].kind == frameArray:
				out = append(out, c)
				i++
				stack = stack[:len(stack)-1]
				completeValue()
			case c == 't' || c == 'f' || c == 'n':
				switch c {
				case 't':
					litTarget = "true"
				case 'f':
					litTarget = "false"
				case 'n':
					litTarget = "null"
				}
				out = append(out, c)
				i++
				litMatched = 1
				st = stateLiteral
			case c == '-' || (c >= '0' && c <= '9'):
				numStart = len(out)
				out = append(out, c)
				i++
				st = stateNumber
			default:
				// Not a valid value start.
				if len(stack) == 0 {
					return "", false
				}
				break skipAndDispatch
			}

		case stateObjKey:
			switch c {
			case '"':
				out = append(out, c)
				i++
				isKey = true
				st = stateString
			case '}':
				out = append(out, c)
				i++
				stack = stack[:len(stack)-1]
				completeValue()
			default:
				break skipAndDispatch
			}

		case stateObjColon:
			if c == ':' {
				out = append(out, c)
				i++
				st = stateValue
			} else {
				break skipAndDispatch
			}

		case stateAfterValue:
			top := &stack[len(stack)-1]
			switch top.kind {
			case frameObject:
				switch c {
				case '}':
					out = append(out, c)
					i++
					stack = stack[:len(stack)-1]
					completeValue()
				case ',':
					entryStart := len(out)
					out = append(out, c)
					i++
					top.entryStart = entryStart
					st = stateObjKey
				default:
					break skipAndDispatch
				}
			case frameArray:
				switch c {
				case ']':
					out = append(out, c)
					i++
					stack = stack[:len(stack)-1]
					completeValue()
				case ',':
					entryStart := len(out)
					out = append(out, c)
					i++
					top.entryStart = entryStart
					st = stateValue
				default:
					break skipAndDispatch
				}
			}

		case stateString:
			if escaping {
				if c == 'u' {
					out = append(out, c)
					i++
					unicodeLeft = 4
					escaping = false
				} else {
					out = append(out, c)
					i++
					escaping = false
				}
				continue
			}
			if unicodeLeft > 0 {
				out = append(out, c)
				i++
				unicodeLeft--
				continue
			}
			switch c {
			case '\\':
				escStart = len(out)
				out = append(out, c)
				i++
				escaping = true
			case '"':
				out = append(out, c)
				i++
				if isKey {
					st = stateObjColon
				} else {
					completeValue()
				}
			default:
				out = append(out, c)
				i++
			}

		case stateNumber:
			if isNumberChar(c) {
				out = append(out, c)
				i++
				continue
			}
			// Full number token already present; terminator character
			// belongs to whatever comes next.
			completeValue()

		case stateLiteral:
			if litMatched < len(litTarget) && c == litTarget[litMatched] {
				out = append(out, c)
				i++
				litMatched++
				if litMatched == len(litTarget) {
					completeValue()
				}
				continue
			}
			// Unexpected character mid-literal: stop here and repair below.
			break skipAndDispatch
		}
	}

	// Resolve whatever was left pending when the input ran out.
	switch st {
	case stateValue, stateObjKey, stateObjColon:
		if len(stack) > 0 {
			out = out[:stack[len(stack)-1].entryStart]
		} else if len(out) == 0 {
			return "", false
		}

	case stateString:
		if isKey {
			out = out[:stack[len(stack)-1].entryStart]
		} else {
			if escaping || unicodeLeft > 0 {
				out = out[:escStart]
			}
			out = append(out, '"')
		}

	case stateNumber:
		tok := trimNumber(string(out[numStart:]))
		if tok == "" {
			if len(stack) > 0 {
				out = out[:stack[len(stack)-1].entryStart]
			} else {
				return "", false
			}
		} else {
			out = out[:numStart]
			out = append(out, tok...)
		}

	case stateLiteral:
		if litMatched > 0 {
			out = append(out, litTarget[litMatched:]...)
		} else if len(stack) > 0 {
			out = out[:stack[len(stack)-1].entryStart]
		} else {
			return "", false
		}

	case stateAfterValue:
		// Value already complete; nothing to trim.
	}

	// Close every still-open container, innermost first.
	for k := len(stack) - 1; k >= 0; k-- {
		if stack[k].kind == frameObject {
			out = append(out, '}')
		} else {
			out = append(out, ']')
		}
	}

	if len(out) == 0 {
		return "", false
	}
	return string(out), true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isNumberChar(c byte) bool {
	switch c {
	case '-', '+', '.', 'e', 'E':
		return true
	default:
		return c >= '0' && c <= '9'
	}
}

// trimNumber returns the longest prefix of tok that forms a complete,
// syntactically valid JSON number, or "" if no such prefix exists (e.g. a
// bare "-" with no digits).
func trimNumber(tok string) string {
	n := len(tok)
	i := 0
	if i < n && tok[i] == '-' {
		i++
	}
	if i >= n || tok[i] < '0' || tok[i] > '9' {
		return ""
	}
	if tok[i] == '0' {
		i++
	} else {
		for i < n && tok[i] >= '0' && tok[i] <= '9' {
			i++
		}
	}
	valid := i

	j := i
	if j < n && tok[j] == '.' {
		k := j + 1
		for k < n && tok[k] >= '0' && tok[k] <= '9' {
			k++
		}
		if k > j+1 {
			valid = k
			j = k
		}
	}

	if j < n && (tok[j] == 'e' || tok[j] == 'E') {
		k := j + 1
		if k < n && (tok[k] == '+' || tok[k] == '-') {
			k++
		}
		m := k
		for m < n && tok[m] >= '0' && tok[m] <= '9' {
			m++
		}
		if m > k {
			valid = m
		}
	}

	return tok[:valid]
}
