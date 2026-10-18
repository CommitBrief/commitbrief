// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

// sarifFixture covers every shape the renderer branches on: a ranged
// finding, a single-line one, a file-level one (Line 0), a Windows-style
// path, a security rule, an empty rule id, and a path whose characters would
// corrupt a URI reference unless percent-encoded.
func sarifFixture() []Finding {
	return []Finding{
		{
			Severity: SeverityCritical, File: "internal/auth/token.go", Line: 42, LineEnd: 47,
			Title: "Token compared with ==", Description: "Use a constant-time comparison.",
			Suggestion: "Use subtle.ConstantTimeCompare.",
			RuleID:     "sec.injection", Fingerprint: "aaaaaaaaaaaaaaaa",
		},
		{
			Severity: SeverityMedium, File: `internal\store\db.go`, Line: 10,
			Title: "Error ignored", Description: "The error from Close is dropped.",
			RuleID: "correctness.error-handling", Fingerprint: "bbbbbbbbbbbbbbbb",
		},
		{
			Severity: SeverityInfo, File: "README.md", Line: 0,
			Title: "Stale section", Description: "The install section is outdated.",
			RuleID: "docs.mismatch", Fingerprint: "cccccccccccccccc",
		},
		{
			Severity: SeverityLow, File: "./main.go", Line: 5, LineEnd: 5,
			Title: "Odd naming", Description: "Name does not follow convention.",
			RuleID: "", Fingerprint: "dddddddddddddddd",
		},
		{
			Severity: SeverityLow, File: "docs/C# notes/100%: a?b.md", Line: 2,
			Title: "Odd path", Description: "The path needs percent-encoding.",
			RuleID: "docs.mismatch", Fingerprint: "eeeeeeeeeeeeeeee",
		},
	}
}

func renderSARIF(t *testing.T, findings []Finding, toolVersion string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := writeSARIF(&buf, findings, toolVersion); err != nil {
		t.Fatalf("writeSARIF: %v", err)
	}
	return buf.Bytes()
}

func decodeSARIF(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	return doc
}

func TestSARIFGolden(t *testing.T) {
	got := renderSARIF(t, sarifFixture(), "1.19.0")
	goldenPath := filepath.Join("testdata", "sarif", "v1.golden")
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
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("SARIF output differs from %s; re-run with -update after reviewing.\n\nGot:\n%s", goldenPath, got)
	}
}

// TestSARIFRequiredFields checks the SARIF 2.1.0 structure GitHub Code
// Scanning requires. The schema is not vendored and no validator dependency
// is added for it, so the required members and enums are asserted by hand.
func TestSARIFRequiredFields(t *testing.T) {
	doc := decodeSARIF(t, renderSARIF(t, sarifFixture(), "1.19.0"))

	if doc["version"] != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", doc["version"])
	}
	if s, _ := doc["$schema"].(string); !strings.HasSuffix(s, "sarif-2.1.0.json") {
		t.Errorf("$schema = %v", doc["$schema"])
	}
	runs, ok := doc["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %v, want exactly one run", doc["runs"])
	}
	run := runs[0].(map[string]any)

	driver := run["tool"].(map[string]any)["driver"].(map[string]any)
	if driver["name"] != "CommitBrief" {
		t.Errorf("driver.name = %v", driver["name"])
	}
	if driver["semanticVersion"] != "1.19.0" {
		t.Errorf("driver.semanticVersion = %v", driver["semanticVersion"])
	}
	if driver["informationUri"] != "https://commitbrief.com" {
		t.Errorf("driver.informationUri = %v", driver["informationUri"])
	}
	if run["automationDetails"].(map[string]any)["id"] != "commitbrief/" {
		t.Errorf("automationDetails.id = %v", run["automationDetails"])
	}

	// rules[] is the full taxonomy in canonical order.
	taxonomy := rules.Taxonomy()
	ruleList := driver["rules"].([]any)
	if len(ruleList) != len(taxonomy) {
		t.Fatalf("rules = %d, want %d", len(ruleList), len(taxonomy))
	}
	levels := map[string]bool{"error": true, "warning": true, "note": true}
	for i, r := range ruleList {
		rm := r.(map[string]any)
		if rm["id"] != taxonomy[i].ID {
			t.Errorf("rules[%d].id = %v, want %s", i, rm["id"], taxonomy[i].ID)
		}
		for _, k := range []string{"name", "shortDescription", "fullDescription", "help", "defaultConfiguration", "properties"} {
			if _, ok := rm[k]; !ok {
				t.Errorf("rules[%d] (%s) missing %q", i, taxonomy[i].ID, k)
			}
		}
		for _, k := range []string{"shortDescription", "fullDescription", "help"} {
			if txt, _ := rm[k].(map[string]any)["text"].(string); txt == "" {
				t.Errorf("rules[%d].%s.text is empty", i, k)
			}
		}
		lvl, _ := rm["defaultConfiguration"].(map[string]any)["level"].(string)
		if !levels[lvl] {
			t.Errorf("rules[%d] level %q not a SARIF level", i, lvl)
		}
		props := rm["properties"].(map[string]any)
		tags := props["tags"].([]any)
		isSec := strings.HasPrefix(taxonomy[i].ID, "sec.")
		hasSecurity := false
		for _, tg := range tags {
			if tg == "security" {
				hasSecurity = true
			}
		}
		if isSec != hasSecurity {
			t.Errorf("rules[%d] %s: security tag = %v, want %v", i, taxonomy[i].ID, hasSecurity, isSec)
		}
		if tags[0] != taxonomy[i].Category() {
			t.Errorf("rules[%d] first tag = %v, want category %q", i, tags[0], taxonomy[i].Category())
		}
		if props["precision"] != "medium" {
			t.Errorf("rules[%d] precision = %v", i, props["precision"])
		}
	}

	results := run["results"].([]any)
	if len(results) != len(sarifFixture()) {
		t.Fatalf("results = %d, want %d", len(results), len(sarifFixture()))
	}
	allowedResultKeys := map[string]bool{
		"ruleId": true, "ruleIndex": true, "level": true, "rank": true, "message": true,
		"locations": true, "partialFingerprints": true, "properties": true,
	}
	for i, r := range results {
		rm := r.(map[string]any)
		for k := range rm {
			if !allowedResultKeys[k] {
				t.Errorf("results[%d] has unexpected key %q", i, k)
			}
		}
		if _, bad := rm["partialFingerprints"].(map[string]any)["primaryLocationLineHash"]; bad {
			t.Errorf("results[%d] sets primaryLocationLineHash; GitHub computes it", i)
		}
		if _, bad := rm["properties"].(map[string]any)["security-severity"]; bad {
			t.Errorf("results[%d] sets security-severity", i)
		}
		idx := int(rm["ruleIndex"].(float64))
		if idx < 0 || idx >= len(ruleList) || ruleList[idx].(map[string]any)["id"] != rm["ruleId"] {
			t.Errorf("results[%d] ruleIndex %d does not point at %v", i, idx, rm["ruleId"])
		}
		if !levels[rm["level"].(string)] {
			t.Errorf("results[%d] level %v", i, rm["level"])
		}
		if rank := rm["rank"].(float64); rank < 0 || rank > 100 {
			t.Errorf("results[%d] rank %v outside 0..100", i, rank)
		}
		if txt, _ := rm["message"].(map[string]any)["text"].(string); txt == "" {
			t.Errorf("results[%d] message.text empty", i)
		}
		locs := rm["locations"].([]any)
		if len(locs) != 1 {
			t.Fatalf("results[%d] locations = %d", i, len(locs))
		}
		phys := locs[0].(map[string]any)["physicalLocation"].(map[string]any)
		art := phys["artifactLocation"].(map[string]any)
		if art["uriBaseId"] != "%SRCROOT%" {
			t.Errorf("results[%d] uriBaseId = %v", i, art["uriBaseId"])
		}
		if uri, _ := art["uri"].(string); uri == "" || strings.Contains(uri, `\`) || strings.HasPrefix(uri, "./") || strings.HasPrefix(uri, "/") {
			t.Errorf("results[%d] uri %q must be repo-relative with /", i, uri)
		}
		region := phys["region"].(map[string]any)
		start := region["startLine"].(float64)
		if start < 1 {
			t.Errorf("results[%d] startLine %v < 1", i, start)
		}
		if end, ok := region["endLine"].(float64); ok && end <= start {
			t.Errorf("results[%d] endLine %v <= startLine %v", i, end, start)
		}
		fp := rm["partialFingerprints"].(map[string]any)
		if s, _ := fp["commitbrief/v1"].(string); s == "" {
			t.Errorf("results[%d] missing commitbrief/v1 fingerprint", i)
		}
	}
}

func TestSARIFWindowsPathUsesForwardSlash(t *testing.T) {
	doc := decodeSARIF(t, renderSARIF(t, []Finding{{
		Severity: SeverityHigh, File: `internal\store\db.go`, Line: 3,
		Title: "t", Description: "d", RuleID: "correctness.error-handling", Fingerprint: "ffffffffffffffff",
	}}, "1.0.0"))
	res := doc["runs"].([]any)[0].(map[string]any)["results"].([]any)[0].(map[string]any)
	uri := res["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["artifactLocation"].(map[string]any)["uri"]
	if uri != "internal/store/db.go" {
		t.Errorf("uri = %v, want internal/store/db.go", uri)
	}
}

func TestSARIFURIIsPercentEncoded(t *testing.T) {
	cases := map[string]string{
		"internal/auth/token.go":     "internal/auth/token.go",
		"docs/C# notes/100%: a?b.md": "docs/C%23%20notes/100%25%3A%20a%3Fb.md",
		"C:/x/y.go":                  "C%3A/x/y.go",
		`dir\sub dir\f.go`:           "dir/sub%20dir/f.go",
		"./src/\u00fcnic\u00f6de.go": "src/%C3%BCnic%C3%B6de.go",
	}
	for in, want := range cases {
		got := sarifURI(in)
		if got != want {
			t.Errorf("sarifURI(%q) = %q, want %q", in, got, want)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("sarifURI(%q) = %q does not parse: %v", in, got, err)
			continue
		}
		if u.Scheme != "" || u.Fragment != "" || u.RawQuery != "" || u.Host != "" {
			t.Errorf("sarifURI(%q) = %q parsed with scheme/host/query/fragment: %+v", in, got, u)
		}
		// Decoding must give back the canonical path.
		if want := strings.TrimPrefix(strings.ReplaceAll(in, `\`, "/"), "./"); u.Path != want {
			t.Errorf("sarifURI(%q) decodes to %q, want %q", in, u.Path, want)
		}
	}
}

func TestSARIFSeverityMapping(t *testing.T) {
	cases := []struct {
		sev   Severity
		level string
		rank  float64
	}{
		{SeverityCritical, "error", 90},
		{SeverityHigh, "error", 70},
		{SeverityMedium, "warning", 50},
		{SeverityLow, "note", 30},
		{SeverityInfo, "note", 10},
	}
	for _, c := range cases {
		t.Run(string(c.sev), func(t *testing.T) {
			doc := decodeSARIF(t, renderSARIF(t, []Finding{{
				Severity: c.sev, File: "a.go", Line: 1, Title: "t", Description: "d",
				RuleID: "correctness.error-handling", Fingerprint: "1111111111111111",
			}}, "1.0.0"))
			res := doc["runs"].([]any)[0].(map[string]any)["results"].([]any)[0].(map[string]any)
			if res["level"] != c.level || res["rank"] != c.rank {
				t.Errorf("level/rank = %v/%v, want %s/%v", res["level"], res["rank"], c.level, c.rank)
			}
			if res["properties"].(map[string]any)["commitbrief/severity"] != string(c.sev) {
				t.Errorf("commitbrief/severity = %v", res["properties"])
			}
		})
	}
}

func TestSARIFFileLevelFinding(t *testing.T) {
	doc := decodeSARIF(t, renderSARIF(t, []Finding{{
		Severity: SeverityLow, File: "a.go", Line: 0, LineEnd: 9, Title: "t", Description: "d",
		RuleID: "docs.mismatch", Fingerprint: "2222222222222222",
	}}, "1.0.0"))
	res := doc["runs"].([]any)[0].(map[string]any)["results"].([]any)[0].(map[string]any)
	region := res["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["region"].(map[string]any)
	if region["startLine"] != float64(1) {
		t.Errorf("startLine = %v, want 1", region["startLine"])
	}
	if _, ok := region["endLine"]; ok {
		t.Errorf("file-level finding must not carry endLine: %v", region)
	}
	if res["properties"].(map[string]any)["commitbrief/file-level"] != true {
		t.Errorf("missing commitbrief/file-level: %v", res["properties"])
	}
}

func TestSARIFUnknownRuleFallsBackToOther(t *testing.T) {
	doc := decodeSARIF(t, renderSARIF(t, []Finding{
		{Severity: SeverityLow, File: "a.go", Line: 1, Title: "t", Description: "d", RuleID: "", Fingerprint: "3333333333333333"},
		{Severity: SeverityLow, File: "a.go", Line: 2, Title: "t", Description: "d", RuleID: "made.up-rule", Fingerprint: "4444444444444444"},
	}, "1.0.0"))
	for i, r := range doc["runs"].([]any)[0].(map[string]any)["results"].([]any) {
		rm := r.(map[string]any)
		if rm["ruleId"] != rules.RuleOther {
			t.Errorf("results[%d].ruleId = %v, want %s", i, rm["ruleId"], rules.RuleOther)
		}
	}
}

func TestSARIFEmptyFindingsEmitsEmptyResults(t *testing.T) {
	for name, in := range map[string][]Finding{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			raw := renderSARIF(t, in, "1.0.0")
			if !bytes.Contains(raw, []byte(`"results": []`)) {
				t.Errorf("results must be an empty array, not null:\n%s", raw)
			}
		})
	}
}

func TestSARIFDevVersionOmitsSemanticVersion(t *testing.T) {
	for _, v := range []string{"dev", ""} {
		raw := renderSARIF(t, nil, v)
		if bytes.Contains(raw, []byte("semanticVersion")) {
			t.Errorf("version %q must not produce semanticVersion", v)
		}
	}
	if raw := renderSARIF(t, nil, "v1.2.3"); !bytes.Contains(raw, []byte(`"semanticVersion": "1.2.3"`)) {
		t.Errorf("leading v must be trimmed:\n%s", raw)
	}
}

func TestSARIFMessageComposition(t *testing.T) {
	got := sarifMessage(Finding{Title: "T", Description: "D", Suggestion: "S"})
	if got != "T — D\nSuggestion: S" {
		t.Errorf("message = %q", got)
	}
	if got := sarifMessage(Finding{Title: "T", Description: "D"}); got != "T — D" {
		t.Errorf("message without suggestion = %q", got)
	}
}

func TestSARIFPayloadWrapperUsesFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, Payload{Findings: sarifFixture()}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("internal/store/db.go")) {
		t.Errorf("SARIF(Payload) did not render payload findings:\n%s", buf.String())
	}
}
