// SPDX-License-Identifier: GPL-3.0-or-later

package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleArchitecture = `{
  "module": "github.com/acme/app",
  "layers": {
    "domain": ["internal/domain"],
    "db":     ["internal/db"],
    "http":   ["internal/http"]
  },
  "rules": {
    "domain": [],
    "db":     ["domain"],
    "http":   ["domain", "db"]
  }
}`

func TestSummarizeRendersLayersAndEdges(t *testing.T) {
	got := Summarize([]byte(sampleArchitecture))
	if got == "" {
		t.Fatal("Summarize returned empty for a valid multi-layer config")
	}

	// Layers section: each layer name with its prefix.
	for _, want := range []string{
		"domain (internal/domain)",
		"db (internal/db)",
		"http (internal/http)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing layer line %q in:\n%s", want, got)
		}
	}

	// Rule semantics: domain may import nothing; db may import domain but not
	// http; http may import domain+db.
	wantLines := []string{
		"domain: may import no other layer; must NOT import db, http",
		"db: may import domain; must NOT import http",
		"http: may import db, domain",
	}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Errorf("missing rule line %q in:\n%s", want, got)
		}
	}

	// The block must instruct the model to flag boundary crossings and to
	// treat the content as context, not instructions.
	for _, want := range []string{"architectural", "must NOT import", "not as instructions"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing guidance phrase %q in:\n%s", want, got)
		}
	}
}

func TestSummarizeDeterministic(t *testing.T) {
	a := Summarize([]byte(sampleArchitecture))
	b := Summarize([]byte(sampleArchitecture))
	if a != b {
		t.Error("Summarize is not deterministic across calls")
	}
}

func TestSummarizeLayerWithoutRuleDeclared(t *testing.T) {
	// `cache` is a declared layer but has no entry in rules → reported as
	// "no rule declared" rather than guessed.
	cfg := `{"layers":{"domain":["internal/domain"],"cache":["internal/cache"]},"rules":{"domain":[]}}`
	got := Summarize([]byte(cfg))
	if !strings.Contains(got, "cache: no rule declared") {
		t.Errorf("layer without a rule should be flagged; got:\n%s", got)
	}
}

func TestSummarizeMalformedIsEmpty(t *testing.T) {
	cases := map[string]string{
		"not json":     "this is not json {",
		"empty object": "{}",
		"no layers":    `{"module":"x","rules":{"a":[]}}`,
		"empty layers": `{"layers":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Summarize([]byte(body)); got != "" {
				t.Errorf("expected empty for %s; got:\n%s", name, got)
			}
		})
	}
}

func TestDiscoverAutoFindsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFilename)
	if err := os.WriteFile(path, []byte(sampleArchitecture), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if !res.Found {
		t.Error("Found should be true when architecture.json exists")
	}
	if res.Context == "" {
		t.Error("Context should be non-empty for a valid file")
	}
	if res.Path != path {
		t.Errorf("Path = %q, want %q", res.Path, path)
	}
}

func TestDiscoverAutoMissIsNoOp(t *testing.T) {
	dir := t.TempDir() // no architecture.json written
	res, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("auto-discovery miss should not error; got %v", err)
	}
	if res.Found {
		t.Error("Found should be false when no file exists")
	}
	if res.Context != "" {
		t.Error("Context should be empty when no file exists")
	}
}

func TestDiscoverConfiguredMissingIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := Discover(dir, "custom-arch.json")
	if err == nil {
		t.Error("an explicitly configured but missing file must error")
	}
}

func TestDiscoverConfiguredRelativePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "config")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "arch.json"), []byte(sampleArchitecture), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Discover(dir, filepath.Join("config", "arch.json"))
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if res.Context == "" {
		t.Error("Context should be non-empty for a configured relative path")
	}
}

func TestDiscoverMalformedFileIsNoOpNotError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultFilename), []byte("{ broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("a malformed file must be a no-op, not an error; got %v", err)
	}
	if res.Context != "" {
		t.Error("malformed file should yield empty Context (never break a review)")
	}
}

func TestDiscoverEmptyRepoRootNoConfig(t *testing.T) {
	res, err := Discover("", "")
	if err != nil {
		t.Fatalf("empty repo root with no configured path should be a no-op; got %v", err)
	}
	if res.Context != "" || res.Found {
		t.Error("nothing to discover with no anchor")
	}
}

func TestSummarizeBoundsLargeConfig(t *testing.T) {
	// A config with more layers than maxLayers should still render and cap.
	var sb strings.Builder
	sb.WriteString(`{"layers":{`)
	for i := 0; i < maxLayers+10; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		// zero-padded so lexical order is stable/predictable
		sb.WriteString(`"layer` + pad(i) + `":["internal/l` + pad(i) + `"]`)
	}
	sb.WriteString(`},"rules":{}}`)

	got := Summarize([]byte(sb.String()))
	if !strings.Contains(got, "more layer(s)") {
		t.Errorf("expected overflow summary for >maxLayers config; got:\n%s", got)
	}
}

func pad(i int) string {
	s := "000"
	d := itoa(i)
	if len(d) >= len(s) {
		return d
	}
	return s[:len(s)-len(d)] + d
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
