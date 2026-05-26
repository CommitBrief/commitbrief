package render

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// SchemaVersion is the integer in the "schema" field of every JSON document
// we emit. Consumers should refuse to parse if SchemaVersion is higher than
// what they expect.
//
// Versioning policy (see docs/json-schema.md):
//   - v1 (current): top-level shape locked at {schema, content, findings,
//     summary, meta}. `findings` and `summary` are reserved arrays/objects;
//     v1 always emits them empty, and v1.x consumers MUST tolerate non-empty
//     additions of well-known shapes within them.
//   - Additive changes (new optional fields in `meta`, new keys in
//     `summary`, new elements in `findings`) are NOT a version bump.
//     Consumers MUST ignore unknown fields.
//   - Renaming, removing, or changing the type of any documented field IS
//     a breaking change and requires bumping SchemaVersion to 2.
const SchemaVersion = 1

type jsonDocument struct {
	Schema   int      `json:"schema"`
	Content  string   `json:"content"`
	Findings []any    `json:"findings"`
	Summary  Summary  `json:"summary"`
	Meta     jsonMeta `json:"meta"`
}

type Summary struct {
	// Placeholder for Phase 10's structured summary. v1 leaves it empty.
}

type jsonMeta struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Lang      string    `json:"lang"`
	Usage     jsonUsage `json:"usage"`
	Cost      float64   `json:"cost_usd"`
	LatencyMS int64     `json:"latency_ms"`
	Cached    bool      `json:"cached"`
	Timestamp time.Time `json:"timestamp"`
}

type jsonUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

func JSON(w io.Writer, p Payload) error {
	doc := jsonDocument{
		Schema:   SchemaVersion,
		Content:  p.Content,
		Findings: []any{},
		Meta: jsonMeta{
			Provider:  p.Meta.Provider,
			Model:     p.Meta.Model,
			Lang:      p.Meta.Lang,
			Cost:      p.Meta.Cost,
			LatencyMS: p.Meta.Latency.Milliseconds(),
			Cached:    p.Meta.Cached,
			Timestamp: p.Meta.Timestamp,
			Usage: jsonUsage{
				InputTokens:       p.Meta.Usage.InputTokens,
				OutputTokens:      p.Meta.Usage.OutputTokens,
				CachedInputTokens: p.Meta.Usage.CachedInputTokens,
			},
		},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("render: encode json: %w", err)
	}
	return nil
}
