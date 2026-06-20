// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcp implements a minimal Model Context Protocol (MCP) server over
// the stdio transport using JSON-RPC 2.0 (ADR-0028). It is deliberately
// stdlib-only (encoding/json + bufio framing) — CommitBrief carries no MCP
// SDK and the surface we expose (initialize, tools/list, tools/call) is small
// enough that a hand-rolled layer is cheaper than a heavy dependency.
//
// The transport is line-delimited JSON: each JSON-RPC message is a single
// object written on its own line and flushed, matching the newline-delimited
// framing every MCP host that speaks stdio understands. We intentionally do
// NOT implement the optional Content-Length header framing — the line form is
// simpler, is what the reference hosts default to over stdio, and keeps the
// reader a plain bufio.Scanner.
package mcp

import "encoding/json"

// ProtocolVersion is the MCP revision this server advertises in the
// initialize response. Kept as a constant so the handshake answer and any
// future negotiation logic share one source of truth.
const ProtocolVersion = "2024-11-05"

// jsonrpcVersion is the fixed "2.0" tag every JSON-RPC envelope carries.
const jsonrpcVersion = "2.0"

// JSON-RPC 2.0 error codes. The first four are the spec-defined reserved
// codes; codeToolError is an application code we return inside a successful
// tools/call envelope is NOT used here — tool failures are reported via the
// result's IsError flag per the MCP spec, not as a JSON-RPC error.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// request is an incoming JSON-RPC 2.0 request (or notification, when ID is
// absent). ID is kept as raw JSON so we can echo it back verbatim — the spec
// allows a string, number, or null, and round-tripping the bytes avoids
// lossy int/float coercion.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the request has no ID. JSON-RPC
// notifications (e.g. "notifications/initialized") MUST NOT be answered.
func (r request) isNotification() bool {
	return len(r.ID) == 0
}

// response is an outgoing JSON-RPC 2.0 response. Exactly one of Result or
// Error is populated; both use omitempty so the wire form matches the spec
// (a success carries "result", a failure carries "error", never both).
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// newResult builds a success response carrying the marshalled result.
func newResult(id json.RawMessage, result any) (response, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return response{}, err
	}
	return response{JSONRPC: jsonrpcVersion, ID: id, Result: raw}, nil
}

// newError builds an error response with the given JSON-RPC code + message.
func newError(id json.RawMessage, code int, message string) response {
	return response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// --- MCP method payloads (the subset we implement) ---

// initializeResult answers the `initialize` handshake: the negotiated
// protocol version, the capabilities we support, and our server identity.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

// serverCapabilities advertises which feature groups the server supports.
// We expose tools only; the empty toolsCapability object signals "tools are
// available" without the optional listChanged subscription.
type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	// ListChanged would advertise that we emit notifications/tools/list_changed.
	// Our tool set is static for the process lifetime, so we omit it.
	ListChanged bool `json:"listChanged,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// listToolsResult is the `tools/list` answer.
type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

// Tool is one MCP tool advertised to the host. InputSchema is a JSON Schema
// object describing the tool's arguments; we marshal it from a Go map so the
// schema lives next to the handler (see tools.go).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// callToolParams is the `tools/call` request payload: the tool name plus an
// arbitrary arguments object the handler decodes itself.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// callToolResult is the `tools/call` answer. Content is a list of typed
// content blocks (we emit a text summary block followed by the structured
// JSON block). IsError signals a tool-level failure WITHOUT failing the
// JSON-RPC call itself, per the MCP spec — the host shows the error content
// to the model so it can react.
type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// contentBlock is an MCP content item. We only ever emit "text" blocks
// (the spec's lowest common denominator, understood by every host): the
// structured findings ride as a JSON string inside a text block, which is the
// conventional way to return machine-readable data over MCP today.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// textContent wraps a string in a single text content block.
func textContent(text string) contentBlock {
	return contentBlock{Type: "text", Text: text}
}
