// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// updateGolden re-writes testdata golden files instead of asserting against
// them. Run with `go test ./internal/render -update` after an intentional
// schema change.
var updateGolden = flag.Bool("update", false, "regenerate golden test files")

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

// TestJSONv1Golden is the drift guard for the v1 JSON schema. Any change to
// JSON output bytes (field rename, type change, ordering, formatting) trips
// this test. If the change is intentional and v1-compatible (additive only —
// see docs/json-schema.md), bump nothing but re-run with -update. If the
// change is breaking, bump SchemaVersion to 2 and document in the policy.
func TestJSONv1Golden(t *testing.T) {
	var w bytes.Buffer
	if err := JSON(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	got := w.Bytes()

	goldenPath := filepath.Join("testdata", "json", "v1.golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create)", err)
	}
	// Normalize CRLF → LF before comparison: git on Windows may have
	// rewritten the .golden file with CRLF line endings depending on
	// core.autocrlf. .gitattributes pins these files to LF for new
	// checkouts; this strip keeps the test green on any existing
	// CRLF-checked-out copy too.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("JSON output differs from %s.\nIf intentional, re-run with -update and review the diff carefully — any non-additive change requires bumping SchemaVersion.\n\nGot:\n%s\n\nWant:\n%s", goldenPath, got, want)
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

func TestExportedLineWrappers(t *testing.T) {
	// HeaderLine / StatusLine / FooterLine must produce the same content
	// the Cards renderer embeds, so non-card surfaces (remote pr) print
	// identical context lines.
	m := Meta{
		Provider: "anthropic", Model: "claude-opus-4-7",
		Files: 3, LinesAdded: 42, LinesRemoved: 7,
		Usage: provider.Usage{InputTokens: 1000, OutputTokens: 840},
		Cost:  0.0042,
	}
	if got := stripANSI(HeaderLine(m)); !strings.Contains(got, "commitbrief") || !strings.Contains(got, "anthropic/claude-opus-4-7") {
		t.Errorf("HeaderLine missing expected segments; got %q", got)
	}
	if got := stripANSI(StatusLine(m)); !strings.Contains(got, "3 files") || !strings.Contains(got, "42 added") {
		t.Errorf("StatusLine missing expected segments; got %q", got)
	}
	if got := stripANSI(StatusLine(Meta{})); got != "" {
		t.Errorf("StatusLine with no stats should be empty; got %q", got)
	}
	if got := stripANSI(FooterLine(m, []Finding{{Severity: SeverityHigh}})); !strings.Contains(got, "Done in") || !strings.Contains(got, "1 finding") {
		t.Errorf("FooterLine missing expected segments; got %q", got)
	}
}

func TestCardsHeader(t *testing.T) {
	var w bytes.Buffer
	if err := Cards(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	for _, want := range []string{"commitbrief", "provider:", "anthropic/claude-opus-4-7", "cache:"} {
		if !strings.Contains(plain, want) {
			t.Errorf("header missing %q; got:\n%s", want, plain)
		}
	}
}

func TestCardsStatusLineSurfacesCounts(t *testing.T) {
	p := samplePayload()
	p.Meta.Files = 3
	p.Meta.LinesAdded = 42
	p.Meta.LinesRemoved = 7
	p.Meta.RulesLoaded = true

	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	for _, want := range []string{"3 files", "42 added", "7 removed", "COMMITBRIEF.md loaded"} {
		if !strings.Contains(plain, want) {
			t.Errorf("status line missing %q; got:\n%s", want, plain)
		}
	}
}

func TestCardsStatusLineOmittedWhenNoStats(t *testing.T) {
	// samplePayload() has zeroed stats by default, so the status line
	// should not appear. (Footer still does.)
	var w bytes.Buffer
	if err := Cards(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if strings.Contains(plain, "analyzing") {
		t.Errorf("status line should be omitted when stats are zero; got:\n%s", plain)
	}
}

func TestCardsFooterCachedSwitch(t *testing.T) {
	// Uncached: footer shows Cost. Cached: footer shows Saved.
	cases := []struct {
		name    string
		cached  bool
		want    string
		notWant string
	}{
		{"uncached", false, "Cost:", "Saved:"},
		{"cached", true, "Saved:", "Cost:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := samplePayload()
			p.Meta.Cached = c.cached
			var w bytes.Buffer
			if err := Cards(&w, p); err != nil {
				t.Fatal(err)
			}
			plain := stripANSI(w.String())
			if !strings.Contains(plain, c.want) {
				t.Errorf("expected %q; got:\n%s", c.want, plain)
			}
			if strings.Contains(plain, c.notWant) {
				t.Errorf("should not contain %q; got:\n%s", c.notWant, plain)
			}
		})
	}
}

func TestCardsRendersBodyContent(t *testing.T) {
	// The glamour-rendered markdown body must still appear between the
	// header/status and footer.
	var w bytes.Buffer
	if err := Cards(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	for _, want := range []string{"Review", "Looks good"} {
		if !strings.Contains(plain, want) {
			t.Errorf("body missing %q; got:\n%s", want, plain)
		}
	}
}

// stripANSI removes ANSI escape sequences so assertions work regardless of
// whether lipgloss enabled color for the test writer (a *bytes.Buffer is
// not a TTY; lipgloss usually downgrades, but we don't want to depend on
// that exact behavior for our text-content assertions).
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until the terminator (letter in @-~).
			for i += 2; i < len(s); i++ {
				c := s[i]
				if c >= '@' && c <= '~' {
					break
				}
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
