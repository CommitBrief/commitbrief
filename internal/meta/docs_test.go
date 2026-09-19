// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"encoding/json"
	"flag"
	"os"
	"sort"
	"strings"
	"testing"
)

// update rewrites README.md's generated regions from the committed
// surface.json instead of merely checking them. Mirrors internal/render's
// own `-update` golden-file flag (see render_test.go).
var update = flag.Bool("update", false, "rewrite README.md's generated regions from surface.json")

// ---------------------------------------------------------------------
// Regions: marker-syntax errors (the three "silently switches the guard
// off" typos Faz 07 exists to catch)
// ---------------------------------------------------------------------

func TestRegionsRejectsUnterminatedRegion(t *testing.T) {
	md := "before\n<!-- commitbrief:gen providers -->\ncontent\n"
	if _, err := Regions(md); err == nil {
		t.Fatal("expected an error for a region that is never closed, got nil")
	}
}

func TestRegionsRejectsUnterminatedRegionBeforeAnotherStarts(t *testing.T) {
	md := "<!-- commitbrief:gen providers -->\n" +
		"content\n" +
		"<!-- commitbrief:gen env-vars -->\n" +
		"content\n" +
		"<!-- commitbrief:end env-vars -->\n"
	if _, err := Regions(md); err == nil {
		t.Fatal("expected an error when a second region opens before the first closes (nesting), got nil")
	}
}

func TestRegionsRejectsDuplicateRegionName(t *testing.T) {
	md := "<!-- commitbrief:gen providers -->\n" +
		"a\n" +
		"<!-- commitbrief:end providers -->\n" +
		"<!-- commitbrief:gen providers -->\n" +
		"b\n" +
		"<!-- commitbrief:end providers -->\n"
	if _, err := Regions(md); err == nil {
		t.Fatal("expected an error for a region name used twice, got nil")
	}
}

func TestRegionsRejectsEndWithoutStart(t *testing.T) {
	md := "<!-- commitbrief:end providers -->\n"
	if _, err := Regions(md); err == nil {
		t.Fatal("expected an error for an end marker with no matching start, got nil")
	}
}

func TestRegionsRejectsMismatchedEndMarker(t *testing.T) {
	md := "<!-- commitbrief:gen providers -->\n" +
		"content\n" +
		"<!-- commitbrief:end env-vars -->\n"
	if _, err := Regions(md); err == nil {
		t.Fatal("expected an error when the end marker names a different region than the open one, got nil")
	}
}

func TestRegionsAcceptsWellFormedDocument(t *testing.T) {
	md := "before\n" +
		"<!-- commitbrief:gen providers -->\n" +
		"a\nb\n" +
		"<!-- commitbrief:end providers -->\n" +
		"between\n" +
		"<!-- commitbrief:gen env-vars -->\n" +
		"c\n" +
		"<!-- commitbrief:end env-vars -->\n" +
		"after\n"
	regions, err := Regions(md)
	if err != nil {
		t.Fatalf("Regions: %v", err)
	}
	if len(regions) != 2 || regions[0].Name != "providers" || regions[1].Name != "env-vars" {
		t.Fatalf("regions = %+v, want [providers, env-vars] in order", regions)
	}
}

// otherRegions returns well-formed, throwaway `<!-- commitbrief:gen NAME
// -->` regions for every registered renderer EXCEPT except, in a fixed
// order. Apply/Verify now require every registered renderer to have
// exactly one region (Faz 07 review M3), so a test that wants to exercise
// ONE region in isolation still has to hand them a complete document —
// this is that fixture, built from the live `renderers` map so it can
// never itself drift out of sync with which renderers exist.
func otherRegions(except string) string {
	names := make([]string, 0, len(renderers))
	for name := range renderers {
		if name != except {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString("<!-- commitbrief:gen " + name + " -->\n")
		b.WriteString("placeholder\n")
		b.WriteString("<!-- commitbrief:end " + name + " -->\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------
// Apply: unknown renderer name, content replacement, idempotency
// ---------------------------------------------------------------------

func TestApplyRejectsUnknownRendererName(t *testing.T) {
	md := "<!-- commitbrief:gen not-a-real-renderer -->\n" +
		"stale content\n" +
		"<!-- commitbrief:end not-a-real-renderer -->\n"
	if _, err := Apply(md, Surface{}); err == nil {
		t.Fatal("expected an error for a region naming an unregistered renderer, got nil")
	}
}

func TestApplyDoesNotPartiallyApplyOnError(t *testing.T) {
	// One good region, one bad: Apply must fail closed rather than
	// rewriting the good region and silently leaving the bad one stale.
	md := "<!-- commitbrief:gen env-vars -->\n" +
		"stale\n" +
		"<!-- commitbrief:end env-vars -->\n" +
		"<!-- commitbrief:gen bogus -->\n" +
		"stale\n" +
		"<!-- commitbrief:end bogus -->\n"
	out, err := Apply(md, Surface{EnvVars: []EnvVar{{Name: "X", Effect: "Y"}}})
	if err == nil {
		t.Fatal("expected an error because of the unknown \"bogus\" renderer, got nil")
	}
	if out != "" {
		t.Errorf("Apply returned non-empty output alongside an error: %q", out)
	}
}

func TestApplyReplacesRegionContentAndKeepsMarkers(t *testing.T) {
	md := "before\n" +
		"<!-- commitbrief:gen env-vars -->\n" +
		"THIS SHOULD BE REPLACED\n" +
		"<!-- commitbrief:end env-vars -->\n" +
		"after\n" +
		otherRegions("env-vars")
	s := Surface{EnvVars: []EnvVar{{Name: "FOO", Effect: "does a thing.", ConfigKey: "foo.bar"}}}

	out, err := Apply(md, s)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(out, "THIS SHOULD BE REPLACED") {
		t.Errorf("stale content survived Apply:\n%s", out)
	}
	if !strings.Contains(out, "<!-- commitbrief:gen env-vars -->") || !strings.Contains(out, "<!-- commitbrief:end env-vars -->") {
		t.Errorf("markers themselves must survive Apply verbatim:\n%s", out)
	}
	if !strings.Contains(out, "`FOO`") || !strings.Contains(out, "`foo.bar`") {
		t.Errorf("rendered content missing from output:\n%s", out)
	}
	if !strings.Contains(out, "before\n<!-- commitbrief:gen env-vars -->") {
		t.Errorf("content immediately before the region must be untouched:\n%q", out)
	}
	if !strings.Contains(out, "<!-- commitbrief:end env-vars -->\nafter\n") {
		t.Errorf("content immediately after the region must be untouched:\n%q", out)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	md := "<!-- commitbrief:gen env-vars -->\n" +
		"stale\n" +
		"<!-- commitbrief:end env-vars -->\n" +
		otherRegions("env-vars")
	s := Surface{EnvVars: []EnvVar{{Name: "FOO", Effect: "does a thing."}}}

	once, err := Apply(md, s)
	if err != nil {
		t.Fatalf("Apply (1st): %v", err)
	}
	twice, err := Apply(once, s)
	if err != nil {
		t.Fatalf("Apply (2nd): %v", err)
	}
	if once != twice {
		t.Errorf("Apply is not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
	if err := Verify(once, s); err != nil {
		t.Errorf("Verify on already-applied content should pass: %v", err)
	}
}

// ---------------------------------------------------------------------
// Verify: reports drift without mutating
// ---------------------------------------------------------------------

func TestVerifyReportsStaleRegions(t *testing.T) {
	md := "<!-- commitbrief:gen env-vars -->\n" +
		"stale\n" +
		"<!-- commitbrief:end env-vars -->\n"
	s := Surface{EnvVars: []EnvVar{{Name: "FOO", Effect: "does a thing."}}}

	err := Verify(md, s)
	if err == nil {
		t.Fatal("expected Verify to report the stale env-vars region, got nil")
	}
	if !strings.Contains(err.Error(), "env-vars") {
		t.Errorf("Verify error does not name the stale region: %v", err)
	}
}

func TestVerifyPassesOnFreshlyAppliedContent(t *testing.T) {
	md := "<!-- commitbrief:gen env-vars -->\n" +
		"stale\n" +
		"<!-- commitbrief:end env-vars -->\n" +
		otherRegions("env-vars")
	s := Surface{EnvVars: []EnvVar{{Name: "FOO", Effect: "does a thing."}}}

	applied, err := Apply(md, s)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := Verify(applied, s); err != nil {
		t.Errorf("Verify on freshly-applied content should pass, got: %v", err)
	}
}

func TestVerifyPropagatesMarkerErrors(t *testing.T) {
	md := "<!-- commitbrief:end providers -->\n"
	if err := Verify(md, Surface{}); err == nil {
		t.Fatal("expected Verify to propagate a marker-syntax error, got nil")
	}
}

// ---------------------------------------------------------------------
// M3 (Faz 07 review, turn 1): a registered renderer with NO region must
// fail loudly, reproducing the three ways a marker pair silently vanishes
// that the reviewer verified independently all left the OLD Verify green:
// deleting the pair outright, misspelling the marker keyword, and
// indenting both lines. Regions() alone cannot catch any of these — from
// its point of view there is simply no marker there — so the check has to
// live in Apply/Verify, comparing the region names it DID find against the
// full `renderers` registry.
// ---------------------------------------------------------------------

func TestVerifyRejectsDeletedRegion(t *testing.T) {
	// A well-formed document missing the "providers" region entirely —
	// as if its marker pair (and everything between) had been deleted.
	md := otherRegions("providers")
	s := Surface{}

	err := Verify(md, s)
	if err == nil {
		t.Fatal("expected Verify to reject a document with no \"providers\" region, got nil")
	}
	if !strings.Contains(err.Error(), "providers") {
		t.Errorf("Verify error does not name the missing renderer: %v", err)
	}
}

func TestVerifyRejectsMisspelledMarkerKeyword(t *testing.T) {
	// "commitbrief:generate"/"commitbrief:stop" instead of "gen"/"end" —
	// genMarker/endMarker simply do not match, so Regions() sees no
	// marker at all here, not a malformed one.
	md := "<!-- commitbrief:generate providers -->\n" +
		"content\n" +
		"<!-- commitbrief:stop providers -->\n" +
		otherRegions("providers")

	err := Verify(md, Surface{})
	if err == nil {
		t.Fatal("expected Verify to reject a misspelled marker keyword, got nil")
	}
	if !strings.Contains(err.Error(), "providers") {
		t.Errorf("Verify error does not name the missing renderer: %v", err)
	}
}

func TestVerifyRejectsIndentedMarker(t *testing.T) {
	// Both marker lines indented — genMarker/endMarker are anchored at
	// the start of the line ("^<!--"), so leading whitespace also makes
	// them invisible to Regions().
	md := "  <!-- commitbrief:gen providers -->\n" +
		"  content\n" +
		"  <!-- commitbrief:end providers -->\n" +
		otherRegions("providers")

	err := Verify(md, Surface{})
	if err == nil {
		t.Fatal("expected Verify to reject an indented marker pair, got nil")
	}
	if !strings.Contains(err.Error(), "providers") {
		t.Errorf("Verify error does not name the missing renderer: %v", err)
	}
}

func TestApplyRejectsMissingRegion(t *testing.T) {
	md := otherRegions("config-schema")
	if _, err := Apply(md, Surface{}); err == nil {
		t.Fatal("expected Apply to reject a document with no \"config-schema\" region, got nil")
	}
}

// ---------------------------------------------------------------------
// TestDocsInSync: the real thing — README.md's four generated regions
// against the committed surface.json.
//
//	go test ./internal/meta -run TestDocsInSync            # check (CI shape)
//	go test ./internal/meta -run TestDocsInSync -update    # rewrite README.md
// ---------------------------------------------------------------------

const readmePath = "../../README.md"

func loadRealSurface(t *testing.T) Surface {
	t.Helper()
	data, err := os.ReadFile("surface.json")
	if err != nil {
		t.Fatalf("read surface.json: %v (run `go run ./cmd/commitbrief --gen-surface internal/meta/surface.json` first)", err)
	}
	var s Surface
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal surface.json: %v", err)
	}
	return s
}

func TestDocsInSync(t *testing.T) {
	s := loadRealSurface(t)

	original, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}

	// Check mode calls ONLY Verify, not Apply: Apply's own error (e.g. a
	// missing region) is a plain "no registered renderer"/"no region"
	// message, while Verify's is the richer one — stale regions AND
	// missing regions, together, with the -update hint. Calling Apply
	// first and t.Fatalf-ing on its error (the previous shape of this
	// test) meant a missing-region problem never reached Verify at all,
	// masking the more useful message behind the terser one (Faz 07
	// review N6).
	if !*update {
		if err := Verify(string(original), s); err != nil {
			t.Errorf("%v\n\n(run `go test ./internal/meta -run TestDocsInSync -update`, then `git diff README.md`)", err)
		}
		return
	}

	updated, err := Apply(string(original), s)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if updated != string(original) {
		if err := os.WriteFile(readmePath, []byte(updated), 0o644); err != nil {
			t.Fatalf("write %s: %v", readmePath, err)
		}
		t.Logf("rewrote generated regions in %s", readmePath)
	}
}

// TestMCPToolArgsRowCountMatchesRealSchema pins the specific regression this
// phase fixes: README's MCP table was hand-written and stuck at 8 rows while
// the real tool schema already had 19 arguments.
func TestMCPToolArgsRowCountMatchesRealSchema(t *testing.T) {
	s := loadRealSurface(t)
	if len(s.MCPToolArgs) != 19 {
		t.Errorf("surface.json has %d MCP tool args, want 19 — "+
			"either the schema changed (update this test) or surface.json is stale (regenerate it)",
			len(s.MCPToolArgs))
	}
}
