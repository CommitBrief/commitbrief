// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// mcpResponse is the minimal JSON-RPC response shape the MCP tests decode.
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// runMCPServer drives the real `commitbrief mcp` server end to end over an
// in-memory stdio pair: it chdir's into the fixture repo, feeds the given
// JSON-RPC request lines on stdin, and returns the decoded responses. This is
// the in-memory stdio harness the prompt calls for — the same Serve loop that
// runs against os.Stdin/os.Stdout in production, driven through buffers here.
func runMCPServer(t *testing.T, e *cliEnv, requests ...string) []mcpResponse {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(e.repoRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	cmd := newMCPCmd()
	root := newRootCmd() // ensures the same global flag wiring as production
	root.AddCommand(cmd)
	cmd.SetIn(strings.NewReader(strings.Join(requests, "\n") + "\n"))
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SetContext(t.Context())

	if err := runMCP(cmd); err != nil {
		t.Fatalf("runMCP: %v\nstderr:\n%s", err, e.errOut.String())
	}

	var resps []mcpResponse
	for _, line := range strings.Split(strings.TrimSpace(e.out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r mcpResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("response not valid JSON: %v\n%s", err, line)
		}
		resps = append(resps, r)
	}
	return resps
}

// TestMCPServerEndToEnd exercises the handshake, tool discovery, and a real
// review tool call against the fixture repo + mock provider — no network, fully
// deterministic. It is the table-driven server drive the prompt requires.
func TestMCPServerEndToEnd(t *testing.T) {
	e := newCLIEnv(t)

	resps := runMCPServer(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"review","arguments":{"staged":true}}}`,
	)

	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3 (notification is unanswered)", len(resps))
	}

	// 1) initialize → server info + capabilities.
	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
	}
	decodeResult(t, resps[0], &initRes)
	if initRes.ServerInfo.Name != "commitbrief" {
		t.Errorf("serverInfo.name = %q, want commitbrief", initRes.ServerInfo.Name)
	}
	if initRes.Capabilities.Tools == nil {
		t.Error("initialize must advertise a tools capability")
	}

	// 2) tools/list → exactly the review tool, with an object input schema
	//    that names the documented knobs.
	var listRes struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	decodeResult(t, resps[1], &listRes)
	if len(listRes.Tools) != 1 || listRes.Tools[0].Name != "review" {
		t.Fatalf("tools/list = %+v, want exactly [review]", listRes.Tools)
	}
	if listRes.Tools[0].Description == "" {
		t.Error("review tool description is empty")
	}
	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(listRes.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("inputSchema not valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("inputSchema.type = %q, want object", schema.Type)
	}
	for _, knob := range []string{"staged", "unstaged", "diff", "provider", "fail_on", "min_severity"} {
		if _, ok := schema.Properties[knob]; !ok {
			t.Errorf("inputSchema missing documented property %q", knob)
		}
	}

	// 3) tools/call review → structured findings come back. The mock provider
	//    returns one finding titled "mock review output".
	var callRes struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	decodeResult(t, resps[2], &callRes)
	if callRes.IsError {
		t.Fatalf("review call returned IsError; content: %+v", callRes.Content)
	}
	if len(callRes.Content) != 2 {
		t.Fatalf("review content blocks = %d, want 2 (summary + JSON)", len(callRes.Content))
	}
	// First block: human summary mentioning the finding count.
	if !strings.Contains(callRes.Content[0].Text, "finding") {
		t.Errorf("summary block missing finding count: %q", callRes.Content[0].Text)
	}
	// Second block: the schema-v1 JSON document with the mock finding.
	var doc struct {
		Schema   int `json:"schema"`
		Findings []struct {
			Title    string `json:"title"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(callRes.Content[1].Text), &doc); err != nil {
		t.Fatalf("structured block is not schema-v1 JSON: %v\n%s", err, callRes.Content[1].Text)
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d, want 1", doc.Schema)
	}
	if len(doc.Findings) != 1 || doc.Findings[0].Title != "mock review output" {
		t.Fatalf("findings = %+v, want one mock finding", doc.Findings)
	}
}

// TestMCPReviewFailOnGate verifies the --fail-on gate is reported in the
// summary (not as a tool error) while the findings are still returned: an
// agent should see both the findings and that the gate tripped.
func TestMCPReviewFailOnGate(t *testing.T) {
	e := newCLIEnv(t)
	resps := runMCPServer(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"review","arguments":{"staged":true,"fail_on":"info"}}}`,
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	var callRes struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	decodeResult(t, resps[0], &callRes)
	if callRes.IsError {
		t.Fatal("fail-on gate must NOT be a tool error — findings are valid")
	}
	if !strings.Contains(callRes.Content[0].Text, "GATE FAILED") {
		t.Errorf("summary should report the gate failure; got %q", callRes.Content[0].Text)
	}
	// Findings JSON is still present in the second block.
	if !strings.Contains(callRes.Content[1].Text, "mock review output") {
		t.Errorf("findings should still be returned alongside a gate failure; got %q", callRes.Content[1].Text)
	}
}

// TestMCPReviewUnknownArgIsToolError verifies a misspelled argument is a clear
// tool error (additionalProperties:false / DisallowUnknownFields), not a
// silent no-op.
func TestMCPReviewUnknownArgIsToolError(t *testing.T) {
	e := newCLIEnv(t)
	resps := runMCPServer(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"review","arguments":{"failon":"info"}}}`,
	)
	var callRes struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	decodeResult(t, resps[0], &callRes)
	if !callRes.IsError {
		t.Fatal("an unknown argument should produce a tool error")
	}
}

// TestMCPCommandRegistered guards the subcommand wiring + NoArgs contract.
func TestMCPCommandRegistered(t *testing.T) {
	cmd := newMCPCmd()
	if cmd.Name() != "mcp" {
		t.Fatalf("command name = %q, want mcp", cmd.Name())
	}
	if cmd.Args == nil {
		t.Error("mcp command should declare an Args validator (NoArgs)")
	}
}

func decodeResult(t *testing.T, r mcpResponse, v any) {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("response carried a JSON-RPC error: %+v", r.Error)
	}
	if err := json.Unmarshal(r.Result, v); err != nil {
		t.Fatalf("decode result: %v\n%s", err, r.Result)
	}
}
