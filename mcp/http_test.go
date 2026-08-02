package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTransportDirectJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Accept") != "application/json, text/event-stream" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	var got struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("id = %d, want 1", got.ID)
	}
}

func TestHTTPTransportSSEMultipleMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support Flush")
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"n\":1}}\n\n")
		fl.Flush()
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"n\":2}}\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","method":"batch"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var ids []int
	for i := 0; i < 2; i++ {
		msg, err := tr.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive[%d]: %v", i, err)
		}
		var got struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(msg, &got); err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		ids = append(ids, got.ID)
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v, want [1 2] (order preserved)", ids)
	}
}

func TestHTTPTransportSessionIDEcho(t *testing.T) {
	const sessionID = "sess-abc-123"
	var gotSessionOnSecond string
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		gotSessionOnSecond = r.Header.Get("Mcp-Session-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive 1: %v", err)
	}
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"b"}`)); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive 2: %v", err)
	}
	if gotSessionOnSecond != sessionID {
		t.Fatalf("second request Mcp-Session-Id = %q, want %q", gotSessionOnSecond, sessionID)
	}
}

func TestHTTPTransportExtraHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, map[string]string{"Authorization": "Bearer tok123"})
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer tok123")
	}
}

func TestHTTPTransportNotificationNoMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// No message should have been queued; Receive should time out.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	if _, err := tr.Receive(shortCtx); err == nil {
		t.Fatal("Receive returned a message after a notification response, want timeout error")
	}
}

// TestHTTPTransportSSEBodyStaysOpen exercises the deviation-from-SHOULD case
// the doc comment on NewStreamableHTTPTransport calls out: a server that
// sends its JSON-RPC response over SSE but keeps the response body open
// afterward (permitted, since the spec only says the server SHOULD close
// it). Send must not wedge behind that open body — CallTool must complete
// promptly — and Close must release the drain goroutine blocked reading it.
func TestHTTPTransportSSEBodyStaysOpen(t *testing.T) {
	bodyReleased := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl := w.(http.Flusher)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}],\"isError\":false}}\n\n", *req.ID)
			fl.Flush()
			// Deliberately keep the body open (no return) until the test
			// tells the client to Close, to simulate a server that doesn't
			// close the stream after responding.
			<-bodyReleased
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	c := NewClient(tr)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// CallTool must complete even though the server keeps the SSE body
	// open; Send returning promptly (not blocking on the drain) is what
	// makes this possible.
	callDone := make(chan error, 1)
	go func() {
		res, err := c.CallTool(ctx, "echo", json.RawMessage(`{}`))
		if err == nil && res.Text != "hi" {
			err = fmt.Errorf("Text = %q, want hi", res.Text)
		}
		callDone <- err
	}()
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("CallTool did not complete while the SSE body stayed open")
	}

	// Close must release the drain goroutine blocked reading the still-open
	// body, and return promptly rather than hanging.
	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Close did not return while a drain goroutine was blocked")
	}
	close(bodyReleased)
}

// TestHTTPTransportIntegrationWithClient exercises the transport through a
// full mcp.Client Initialize + CallTool round trip against an httptest
// server that replies with SSE.
func TestHTTPTransportIntegrationWithClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}],\"isError\":false}}\n\n", *req.ID)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	c := NewClient(tr)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "hi" {
		t.Fatalf("Text = %q, want hi", res.Text)
	}
}
