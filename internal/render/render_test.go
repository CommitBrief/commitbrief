package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func samplePayload() Payload {
	return Payload{
		Content: "# Review\n\nLooks good.\n",
		Meta: Meta{
			Provider: "anthropic",
			Model:    "claude-opus-4-7",
			Lang:     "en",
			Usage: provider.Usage{
				InputTokens:       4231,
				OutputTokens:      1503,
				CachedInputTokens: 800,
			},
			Cost:      0.0632,
			Latency:   3200 * time.Millisecond,
			Cached:    false,
			Timestamp: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		},
	}
}

func TestFormatString(t *testing.T) {
	cases := map[Format]string{
		FormatTerminal: "terminal",
		FormatMarkdown: "markdown",
		FormatJSON:     "json",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", f, got, want)
		}
	}
}

func TestMarkdownPlainPassthrough(t *testing.T) {
	var w bytes.Buffer
	if err := Markdown(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, "# Review") {
		t.Errorf("missing heading; got:\n%s", out)
	}
	if strings.Contains(out, "─") {
		t.Errorf("plain markdown should not include verbose footer rule; got:\n%s", out)
	}
}

func TestMarkdownAppendsNewline(t *testing.T) {
	var w bytes.Buffer
	p := Payload{Content: "no trailing newline"}
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(w.String(), "\n") {
		t.Error("Markdown should ensure trailing newline")
	}
}

func TestMarkdownVerboseFooter(t *testing.T) {
	var w bytes.Buffer
	p := samplePayload()
	p.Verbose = true
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	for _, want := range []string{"anthropic", "claude-opus-4-7", "in=4231", "out=1503", "cached: 800", "$0.0632", "3.20s"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose footer missing %q; got:\n%s", want, out)
		}
	}
}

func TestTerminalRenders(t *testing.T) {
	var w bytes.Buffer
	if err := Terminal(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, "Review") {
		t.Errorf("terminal output missing 'Review'; got:\n%s", out)
	}
	if !strings.Contains(out, "Looks good") {
		t.Errorf("terminal output missing content; got:\n%s", out)
	}
}

func TestTerminalVerbose(t *testing.T) {
	var w bytes.Buffer
	p := samplePayload()
	p.Verbose = true
	if err := Terminal(&w, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "Provider:") {
		t.Errorf("verbose footer missing; got:\n%s", w.String())
	}
}

func TestJSONStructure(t *testing.T) {
	var w bytes.Buffer
	if err := JSON(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, w.String())
	}

	if got := doc["schema"]; got != float64(SchemaVersion) {
		t.Errorf("schema = %v, want %d", got, SchemaVersion)
	}
	if !strings.Contains(doc["content"].(string), "Review") {
		t.Errorf("content = %v", doc["content"])
	}
	if _, ok := doc["findings"]; !ok {
		t.Error("findings field missing (must be present even when empty)")
	}
	meta, ok := doc["meta"].(map[string]any)
	if !ok {
		t.Fatal("meta is not an object")
	}
	for _, want := range []string{"provider", "model", "lang", "usage", "cost_usd", "latency_ms", "cached", "timestamp"} {
		if _, ok := meta[want]; !ok {
			t.Errorf("meta missing %q", want)
		}
	}
	usage := meta["usage"].(map[string]any)
	for _, want := range []string{"input_tokens", "output_tokens", "cached_input_tokens"} {
		if _, ok := usage[want]; !ok {
			t.Errorf("usage missing %q", want)
		}
	}
}

func TestJSONLatencyMillis(t *testing.T) {
	var w bytes.Buffer
	if err := JSON(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Bytes(), &doc)
	meta := doc["meta"].(map[string]any)
	if meta["latency_ms"] != float64(3200) {
		t.Errorf("latency_ms = %v, want 3200", meta["latency_ms"])
	}
}

func TestJSONCachedHit(t *testing.T) {
	var w bytes.Buffer
	p := samplePayload()
	p.Meta.Cached = true
	if err := JSON(&w, p); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Bytes(), &doc)
	meta := doc["meta"].(map[string]any)
	if meta["cached"] != true {
		t.Errorf("cached = %v, want true", meta["cached"])
	}
}

func TestVerboseFooterContent(t *testing.T) {
	m := samplePayload().Meta
	out := VerboseFooter(m)
	for _, want := range []string{
		"Provider:  anthropic",
		"Model:     claude-opus-4-7",
		"in=4231, out=1503 (provider cached: 800)",
		"Cost:      $0.0632",
		"Latency:   3.20s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q; got:\n%s", want, out)
		}
	}
}

func TestVerboseFooterCachedMarker(t *testing.T) {
	m := samplePayload().Meta
	m.Cached = true
	out := VerboseFooter(m)
	if !strings.Contains(out, "(local cache hit)") {
		t.Errorf("cached footer missing (local cache hit) marker; got:\n%s", out)
	}
}

func TestVerboseFooterCachedShowsSavedNotCost(t *testing.T) {
	m := samplePayload().Meta
	m.Cached = true
	// Cost is set to what would have been spent — Saved is the right label
	// on a local cache hit (no provider call happened).
	out := VerboseFooter(m)
	if !strings.Contains(out, "Saved:") {
		t.Errorf("cached footer should label cost field as 'Saved'; got:\n%s", out)
	}
	if strings.Contains(out, "Cost:") {
		t.Errorf("cached footer should NOT show 'Cost:' label (use 'Saved:'); got:\n%s", out)
	}
	if !strings.Contains(out, "$0.0632") {
		t.Errorf("Saved amount missing; got:\n%s", out)
	}
}

func TestVerboseFooterUncachedShowsCostNotSaved(t *testing.T) {
	m := samplePayload().Meta
	m.Cached = false
	out := VerboseFooter(m)
	if !strings.Contains(out, "Cost:") {
		t.Errorf("uncached footer should show 'Cost:'; got:\n%s", out)
	}
	if strings.Contains(out, "Saved:") {
		t.Errorf("uncached footer should NOT show 'Saved:'; got:\n%s", out)
	}
}

func TestVerboseFooterOmitsEmptyFields(t *testing.T) {
	out := VerboseFooter(Meta{})
	if strings.Contains(out, "Provider:") {
		t.Error("Provider line should be omitted when empty")
	}
	if strings.Contains(out, "Cost:") {
		t.Error("Cost line should be omitted when zero")
	}
	if strings.Contains(out, "Latency:") {
		t.Error("Latency line should be omitted when zero")
	}
	// Tokens line always present
	if !strings.Contains(out, "Tokens:") {
		t.Error("Tokens line must always be present in footer")
	}
}

func TestFormatDurationBuckets(t *testing.T) {
	cases := map[time.Duration]string{
		3200 * time.Millisecond: "3.20s",
		500 * time.Millisecond:  "500ms",
		200 * time.Microsecond:  "200µs",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
