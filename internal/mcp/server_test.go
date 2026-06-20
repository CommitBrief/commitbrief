// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// driveServer runs a server over an in-memory request/response pair: it joins
// the given JSON-RPC request lines with newlines as stdin and returns each
// non-empty response line, decoded. This is the in-memory stdio harness the
// MCP transport is designed to be testable through.
func driveServer(t *testing.T, s *Server, requests ...string) []response {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	var resps []response
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("response line is not valid JSON: %v\n%s", err, line)
		}
		resps = append(resps, r)
	}
	return resps
}

// newTestServer registers a deterministic `echo` tool that returns its
// arguments verbatim, plus an `explode` tool that always errors — enough to
// cover the success and tool-error branches without any pipeline.
func newTestServer() *Server {
	s := New("commitbrief-test", "0.0.0")
	schema, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"msg": map[string]any{"type": "string"}},
	})
	s.Register(Tool{Name: "echo", Description: "echo back", InputSchema: schema},
		func(_ context.Context, args json.RawMessage) (string, string, error) {
			return "echoed", string(args), nil
		})
	s.Register(Tool{Name: "explode", Description: "always fails", InputSchema: schema},
		func(_ context.Context, _ json.RawMessage) (string, string, error) {
			return "", "", errors.New("boom")
		})
	return s
}

func TestServerHandshakeAndTools(t *testing.T) {
	tests := []struct {
		name    string
		request string
		assert  func(t *testing.T, r response)
	}{
		{
			name:    "initialize",
			request: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`,
			assert: func(t *testing.T, r response) {
				if r.Error != nil {
					t.Fatalf("initialize errored: %+v", r.Error)
				}
				var res initializeResult
				mustUnmarshal(t, r.Result, &res)
				if res.ProtocolVersion != ProtocolVersion {
					t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, ProtocolVersion)
				}
				if res.ServerInfo.Name != "commitbrief-test" {
					t.Errorf("serverInfo.name = %q", res.ServerInfo.Name)
				}
			},
		},
		{
			name:    "tools/list advertises echo with schema",
			request: `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
			assert: func(t *testing.T, r response) {
				if r.Error != nil {
					t.Fatalf("tools/list errored: %+v", r.Error)
				}
				var res listToolsResult
				mustUnmarshal(t, r.Result, &res)
				if len(res.Tools) != 2 {
					t.Fatalf("tools length = %d, want 2", len(res.Tools))
				}
				var echo *Tool
				for i := range res.Tools {
					if res.Tools[i].Name == "echo" {
						echo = &res.Tools[i]
					}
				}
				if echo == nil {
					t.Fatal("echo tool missing from tools/list")
				}
				// The advertised input schema must be a JSON object.
				var schemaObj map[string]any
				mustUnmarshal(t, echo.InputSchema, &schemaObj)
				if schemaObj["type"] != "object" {
					t.Errorf("echo inputSchema.type = %v, want object", schemaObj["type"])
				}
			},
		},
		{
			name:    "tools/call echo returns content",
			request: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`,
			assert: func(t *testing.T, r response) {
				if r.Error != nil {
					t.Fatalf("tools/call errored: %+v", r.Error)
				}
				var res callToolResult
				mustUnmarshal(t, r.Result, &res)
				if res.IsError {
					t.Fatal("echo should not be an error result")
				}
				if len(res.Content) != 2 {
					t.Fatalf("content blocks = %d, want 2 (summary + structured)", len(res.Content))
				}
				if res.Content[0].Text != "echoed" {
					t.Errorf("summary = %q, want echoed", res.Content[0].Text)
				}
				if !strings.Contains(res.Content[1].Text, "hi") {
					t.Errorf("structured block missing args echo: %q", res.Content[1].Text)
				}
			},
		},
		{
			name:    "tools/call error surfaces as IsError content",
			request: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"explode","arguments":{}}}`,
			assert: func(t *testing.T, r response) {
				if r.Error != nil {
					t.Fatalf("a tool failure must not become a JSON-RPC error: %+v", r.Error)
				}
				var res callToolResult
				mustUnmarshal(t, r.Result, &res)
				if !res.IsError {
					t.Fatal("explode should set IsError")
				}
				if !strings.Contains(res.Content[0].Text, "boom") {
					t.Errorf("error content = %q, want it to mention boom", res.Content[0].Text)
				}
			},
		},
		{
			name:    "unknown tool is invalid params",
			request: `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`,
			assert: func(t *testing.T, r response) {
				if r.Error == nil || r.Error.Code != codeInvalidParams {
					t.Fatalf("want invalid-params error, got %+v", r.Error)
				}
			},
		},
		{
			name:    "unknown method is method not found",
			request: `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`,
			assert: func(t *testing.T, r response) {
				if r.Error == nil || r.Error.Code != codeMethodNotFound {
					t.Fatalf("want method-not-found, got %+v", r.Error)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resps := driveServer(t, newTestServer(), tc.request)
			if len(resps) != 1 {
				t.Fatalf("got %d responses, want 1", len(resps))
			}
			tc.assert(t, resps[0])
		})
	}
}

// TestNotificationIsNotAnswered verifies that an id-less request (a JSON-RPC
// notification, e.g. notifications/initialized) produces no response on the
// wire while a following request still gets answered.
func TestNotificationIsNotAnswered(t *testing.T) {
	resps := driveServer(t, newTestServer(),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`,
	)
	if len(resps) != 1 {
		t.Fatalf("notification produced a response; got %d responses", len(resps))
	}
	if string(resps[0].ID) != "9" {
		t.Errorf("answered id = %s, want 9", resps[0].ID)
	}
}

// TestParseErrorOnGarbage verifies a non-JSON line is answered with a
// JSON-RPC parse error rather than tearing down the loop.
func TestParseErrorOnGarbage(t *testing.T) {
	resps := driveServer(t, newTestServer(), `not json at all`)
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Fatalf("want a single parse-error response, got %+v", resps)
	}
}

// TestValidateRequiresTools guards the empty-server configuration error.
func TestValidateRequiresTools(t *testing.T) {
	if err := New("x", "0").Validate(); !errors.Is(err, errNoTools) {
		t.Fatalf("empty server Validate = %v, want errNoTools", err)
	}
	if err := newTestServer().Validate(); err != nil {
		t.Fatalf("populated server Validate = %v, want nil", err)
	}
}

// TestDuplicateRegistrationPanics asserts the startup guard.
func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate tool registration should panic")
		}
	}()
	s := New("x", "0")
	tool := Tool{Name: "dup", InputSchema: json.RawMessage(`{}`)}
	noop := func(context.Context, json.RawMessage) (string, string, error) { return "", "", nil }
	s.Register(tool, noop)
	s.Register(tool, noop) // panics
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
}
