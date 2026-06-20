// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ToolHandler runs one tool invocation. arguments is the raw JSON of the
// host-supplied "arguments" object (may be empty). The handler returns the
// text summary, the structured JSON payload, and an error.
//
// A non-nil error is reported to the host as a tool-level failure
// (callToolResult.IsError = true) carrying err.Error() — NOT as a JSON-RPC
// protocol error, so a failed review (e.g. an aborted secret-scan guard)
// reaches the model as actionable content instead of tearing down the call.
type ToolHandler func(ctx context.Context, arguments json.RawMessage) (summary string, structured string, err error)

// registeredTool pairs a tool's advertised descriptor with its handler.
type registeredTool struct {
	def     Tool
	handler ToolHandler
}

// Server is a minimal MCP server speaking JSON-RPC 2.0 over a line-delimited
// stdio transport (ADR-0028). Construct it with New, register tools with
// Register, then drive the read/dispatch/write loop with Serve. The server is
// single-connection and processes requests sequentially — there is exactly one
// host on the other end of stdio, and a review is a blocking operation, so
// concurrency would buy nothing and complicate the guard/preflight prompts.
type Server struct {
	name    string
	version string
	tools   []registeredTool
}

// New builds a server that identifies itself to the host as name@version.
func New(name, version string) *Server {
	return &Server{name: name, version: version}
}

// Register adds a tool. def.InputSchema must already be valid JSON Schema.
// Registering two tools with the same name panics — it is a programming
// error, caught at startup, never on the wire.
func (s *Server) Register(def Tool, handler ToolHandler) {
	for _, t := range s.tools {
		if t.def.Name == def.Name {
			panic("mcp: duplicate tool registration: " + def.Name)
		}
	}
	s.tools = append(s.tools, registeredTool{def: def, handler: handler})
}

// Serve runs the read → dispatch → write loop until r reaches EOF (the host
// closed stdin) or a write to w fails. It returns nil on a clean EOF so a
// host disconnect is a normal shutdown, not an error. ctx is threaded into
// every tool handler so the host (or a signal) can cancel an in-flight
// review.
//
// Framing: one JSON message per line. We use a bufio.Scanner with an enlarged
// buffer because a tools/call result can carry a sizeable findings document;
// the default 64KiB token cap would truncate large reviews.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	writer := bufio.NewWriter(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue // tolerate blank separator lines between messages
		}
		resp, emit := s.dispatch(ctx, line)
		if !emit {
			continue // notification: no answer on the wire
		}
		if err := writeMessage(writer, resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("mcp: read: %w", err)
	}
	return nil
}

// maxMessageBytes caps a single JSON-RPC line. 16 MiB comfortably holds the
// largest realistic findings document while still bounding memory against a
// hostile or runaway peer.
const maxMessageBytes = 16 * 1024 * 1024

// dispatch parses one raw line and routes it to the matching method handler.
// The bool return reports whether resp should be written: a notification
// (no id) is processed for side effects but never answered, and a parse error
// on a message we cannot even extract an id from is answered with a null-id
// error per JSON-RPC.
func (s *Server) dispatch(ctx context.Context, line []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return newError(nil, codeParseError, "parse error: "+err.Error()), true
	}
	if req.JSONRPC != jsonrpcVersion {
		if req.isNotification() {
			return response{}, false
		}
		return newError(req.ID, codeInvalidRequest, "invalid request: jsonrpc must be \"2.0\""), true
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleListTools(req)
	case "tools/call":
		return s.handleCallTool(ctx, req)
	case "ping":
		// MCP keepalive: an empty object result. Some hosts probe with it.
		resp, _ := newResult(req.ID, struct{}{})
		return resp, !req.isNotification()
	default:
		// "notifications/initialized" and any other notification: ack by
		// silence. An unknown *request* (has an id) gets MethodNotFound.
		if req.isNotification() {
			return response{}, false
		}
		return newError(req.ID, codeMethodNotFound, "method not found: "+req.Method), true
	}
}

func (s *Server) handleInitialize(req request) (response, bool) {
	// We accept the client's protocolVersion implicitly and answer with the
	// version we implement. A full negotiation (rejecting unknown versions)
	// is unnecessary for a single-tool gate; hosts tolerate a server pinning
	// its own supported revision.
	result := initializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    serverCapabilities{Tools: toolsCapability{}},
		ServerInfo:      serverInfo{Name: s.name, Version: s.version},
	}
	resp, err := newResult(req.ID, result)
	if err != nil {
		return newError(req.ID, codeInternalError, err.Error()), true
	}
	return resp, !req.isNotification()
}

func (s *Server) handleListTools(req request) (response, bool) {
	defs := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		defs = append(defs, t.def)
	}
	resp, err := newResult(req.ID, listToolsResult{Tools: defs})
	if err != nil {
		return newError(req.ID, codeInternalError, err.Error()), true
	}
	return resp, !req.isNotification()
}

func (s *Server) handleCallTool(ctx context.Context, req request) (response, bool) {
	if req.isNotification() {
		// A call with no id is malformed (it expects a result); drop it.
		return response{}, false
	}
	var params callToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return newError(req.ID, codeInvalidParams, "invalid params: "+err.Error()), true
	}
	handler := s.lookup(params.Name)
	if handler == nil {
		// Unknown tool name is a protocol-level invalid-params error: the
		// host asked for something we never advertised in tools/list.
		return newError(req.ID, codeInvalidParams, "unknown tool: "+params.Name), true
	}

	summary, structured, err := handler(ctx, params.Arguments)
	if err != nil {
		// Tool-level failure: surface as content with IsError, not a JSON-RPC
		// error. The model sees what went wrong (e.g. "no staged changes",
		// "secret scan aborted") and can adjust instead of the call collapsing.
		errResult := callToolResult{
			Content: []contentBlock{textContent(toolErrorText(err))},
			IsError: true,
		}
		resp, mErr := newResult(req.ID, errResult)
		if mErr != nil {
			return newError(req.ID, codeInternalError, mErr.Error()), true
		}
		return resp, true
	}

	content := []contentBlock{}
	if summary != "" {
		content = append(content, textContent(summary))
	}
	content = append(content, textContent(structured))
	resp, mErr := newResult(req.ID, callToolResult{Content: content})
	if mErr != nil {
		return newError(req.ID, codeInternalError, mErr.Error()), true
	}
	return resp, true
}

func (s *Server) lookup(name string) ToolHandler {
	for _, t := range s.tools {
		if t.def.Name == name {
			return t.handler
		}
	}
	return nil
}

// toolErrorText renders a handler error for the IsError content block. It is
// a thin wrapper today (just err.Error()) but is the single chokepoint where
// any future redaction of provider-internal detail would live.
func toolErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// writeMessage encodes one response as a single line + newline and flushes
// so the host sees it immediately (stdio is interactive; an unflushed buffer
// would deadlock the handshake).
func writeMessage(w *bufio.Writer, resp response) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("mcp: encode response: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("mcp: write response: %w", err)
	}
	if err := w.WriteByte('\n'); err != nil {
		return fmt.Errorf("mcp: write newline: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("mcp: flush: %w", err)
	}
	return nil
}

// errNoTools is returned by Serve's caller path when a server is started with
// no tools registered — a configuration bug worth catching loudly rather than
// silently serving an empty tools/list.
var errNoTools = errors.New("mcp: no tools registered")

// Validate reports a configuration error before Serve is entered. Currently
// it only guards against an empty tool set; kept as a method so the CLI's
// startup can fail fast with a translated message.
func (s *Server) Validate() error {
	if len(s.tools) == 0 {
		return errNoTools
	}
	return nil
}
