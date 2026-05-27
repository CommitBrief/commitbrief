// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

// formatJSON is the value Ollama's /api/chat `format` field accepts to
// constrain output to valid JSON. Larger instruct-tuned models honour it
// reliably; smaller models may still produce malformed output, which the
// renderer-side graceful degrade (ADR-0014 §4) catches.
//
// Unlike Anthropic/OpenAI/Gemini, Ollama does not accept a JSON Schema —
// the model must lean on the <response_format> block in the system prompt
// (assembled in internal/rules) for the actual shape. format:"json" only
// guarantees syntactically-valid JSON output, not schema conformance.
const formatJSON = "json"
