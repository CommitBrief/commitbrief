// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------- PatchField: the three losses (06-config-set-write-path.md) ----------

// (1) Zero-value destruction: patching one key in a partial file must not
// pull in any other field at its Go zero value.
func TestPatchFieldDoesNotAddZeroValuedFields(t *testing.T) {
	path := writeConfig(t, "config.yml", "version: 1\nprovider: mock\n")

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, unwanted := range []string{"secret_scan", "injection_scan", "warn_threshold_usd", "flaky", "cache:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("patched file must not gain unrelated fields; found %q in:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
	if !strings.Contains(out, "provider: mock") {
		t.Errorf("existing provider key lost; file:\n%s", out)
	}
}

// (2) An unknown key's value must survive a patch untouched — this is what
// makes Faz 05's refuse-to-write gate unnecessary once the write path no
// longer decodes through the typed struct.
func TestPatchFieldPreservesUnknownKey(t *testing.T) {
	const body = `version: 1
provider: mock
guard:
  secret_patterns:
    - name: Acme Internal Token
      pattern: 'acme_[A-Za-z0-9]{32}'
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "pattern: 'acme_[A-Za-z0-9]{32}'") {
		t.Errorf("unknown key's value was lost; file:\n%s", out)
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

// (3) A top-level `x-` anchor holder — the tool's own suggested escape hatch
// for a bare YAML anchor — must survive a patch untouched, including when
// it's merged elsewhere via `<<:`.
func TestPatchFieldPreservesXPrefixedAnchorKey(t *testing.T) {
	const body = `version: 1
provider: mock
x-defaults: &d
  ttl_days: 3
cache:
  <<: *d
  enabled: true
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "x-defaults:") {
		t.Errorf("x-defaults anchor holder was lost; file:\n%s", out)
	}
	if !strings.Contains(out, "<<:") {
		t.Errorf("the cache merge key was lost; file:\n%s", out)
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

// ---------- comments, key order, full-field round-trip ----------

func TestPatchFieldPreservesCommentsAndKeyOrder(t *testing.T) {
	const body = `# my note
version: 1
provider: mock
output:
  lang: en
  color: never
cache:
  enabled: true
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"cache", "ttl_days"}, 30); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "# my note") {
		t.Errorf("user comment was dropped; file:\n%s", out)
	}
	// Key order: provider before output before cache, exactly as authored.
	iProvider := strings.Index(out, "provider:")
	iOutput := strings.Index(out, "output:")
	iCache := strings.Index(out, "cache:")
	if !(iProvider < iOutput && iOutput < iCache) {
		t.Errorf("top-level key order changed; file:\n%s", out)
	}
	if !strings.Contains(out, "ttl_days: 30") {
		t.Errorf("cache.ttl_days was not set; file:\n%s", out)
	}
}

// A fully-fielded file (every key config.Default() would produce) must come
// back byte-identical except for the one changed key — the shape a fresh
// `commitbrief setup` run already produces, so an existing user's config
// must never regress under the new write path.
func TestPatchFieldFullyFieldedFileOnlyChangesTargetKey(t *testing.T) {
	full, err := marshalToMap(Default())
	if err != nil {
		t.Fatal(err)
	}
	full["provider"] = "mock"
	beforeBytes, err := yaml.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, "config.yml", string(beforeBytes))

	if err := PatchField(path, []string{"cache", "ttl_days"}, 45); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	beforeLines := strings.Split(strings.TrimRight(string(beforeBytes), "\n"), "\n")
	afterLines := strings.Split(strings.TrimRight(string(after), "\n"), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed: before=%d after=%d\nbefore:\n%s\nafter:\n%s",
			len(beforeLines), len(afterLines), beforeBytes, after)
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] != afterLines[i] {
			changed++
			if !strings.Contains(afterLines[i], "ttl_days: 45") {
				t.Errorf("unexpected changed line %d: before=%q after=%q", i, beforeLines[i], afterLines[i])
			}
		}
	}
	if changed != 1 {
		t.Errorf("want exactly 1 changed line, got %d\nbefore:\n%s\nafter:\n%s", changed, beforeBytes, after)
	}
}

// ---------- nested creation, error handling ----------

func TestPatchFieldCreatesMissingNestedMapping(t *testing.T) {
	path := writeConfig(t, "config.yml", "version: 1\nprovider: mock\n")

	if err := PatchField(path, []string{"providers", "anthropic", "api_key"}, "sk-ant-scripted"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := yaml.Unmarshal(got, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-ant-scripted" {
		t.Errorf("api_key = %q, want sk-ant-scripted", cfg.Providers["anthropic"].APIKey)
	}
}

func TestPatchFieldOnMissingFileCreatesMinimalDoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", got)
	}
}

func TestPatchFieldRejectsEmptyPath(t *testing.T) {
	if err := PatchField("", []string{"output", "lang"}, "tr"); err == nil {
		t.Fatal("want error for empty path")
	}
}

func TestPatchFieldRejectsEmptyKeyPath(t *testing.T) {
	path := writeConfig(t, "config.yml", "version: 1\n")
	if err := PatchField(path, nil, "tr"); err == nil {
		t.Fatal("want error for empty key path")
	}
}

// ---------- Faz 06 review turu 1 ----------

// Item A [BLOCKER]: patching a key whose value node carries an anchor must
// not drop the anchor. Node.Encode fully overwrites the node struct
// (Anchor included); dropping it silently leaves every `*m` alias
// elsewhere in the file dangling — a file that still writes with exit 0
// but no longer PARSES at all afterward (every later command, `config
// show` included, fails "unknown anchor").
func TestPatchFieldPreservesAnchorOnPatchedScalar(t *testing.T) {
	const body = `version: 1
provider: mock
providers:
  mock:
    model: &m sonnet
  other:
    model: *m
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"providers", "mock", "model"}, "opus"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "&m") {
		t.Errorf("anchor &m was dropped, leaving a dangling alias; file:\n%s", out)
	}

	// The real proof: the file must still PARSE. A dangling alias is a
	// parse error, not a decode-into-Config error, so try the exact form a
	// dangling alias fails at.
	var probe yaml.Node
	if err := yaml.Unmarshal(got, &probe); err != nil {
		t.Fatalf("patched file no longer parses (dangling alias?): %v\nfile:\n%s", err, out)
	}

	// A follow-up command must still work on the patched file — `config
	// get` reads through the typed Config, which is exactly what a
	// dangling alias breaks first.
	var cfg Config
	if err := yaml.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("follow-up decode into Config failed: %v\nfile:\n%s", err, out)
	}
	if got := cfg.Providers["mock"].Model; got != "opus" {
		t.Errorf("providers.mock.model = %q, want opus", got)
	}
	// The alias tracks the anchor's new value — expected: `other.model`
	// aliases `mock.model`, so it changes too, exactly as it would if a
	// person hand-edited the `&m` line. This is not something PatchField
	// should try to prevent; it's what the file's author asked for by
	// using an alias instead of an independent value.
	if got := cfg.Providers["other"].Model; got != "opus" {
		t.Errorf("providers.other.model (aliased) = %q, want opus (alias should track the anchor)", got)
	}
}

// Item B [MAJOR]: a multi-document (`---`-separated) file is refused
// outright rather than silently truncated to its first document. Such a
// file loads fine today (config.Load only ever reads the first document
// too), so it is a working config with real content past the separator;
// re-encoding only the first document would delete the rest with no
// warning — the fourth instance of exactly the loss class this fix exists
// to close.
func TestPatchFieldRefusesMultiDocumentFile(t *testing.T) {
	const body = "version: 1\nprovider: mock\n---\n# second document\nx-notes: keep me\n"
	path := writeConfig(t, "config.yml", body)

	err := PatchField(path, []string{"output", "lang"}, "tr")
	if err == nil {
		t.Fatal("want an error for a multi-document file, got nil")
	}
	if !strings.Contains(err.Error(), "more than one YAML document") {
		t.Errorf("error should name the multi-document reason; got: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("file must be left untouched on refusal.\nbefore:\n%s\nafter:\n%s", body, after)
	}
}

// Item H [turu 2]: a decorative trailing `---` with nothing meaningful
// after it must NOT be treated as a second document — not one byte would
// be lost by patching through it, so refusing here is pure friction the
// user can't repair with the tool itself (the whole point of "refuse" over
// "silently lose" is that the tool stays usable; this is where blanket
// refusal stopped being usable). Any comment that visually sits after that
// last `---` is already captured on the FIRST document's FootComment (see
// TestPatchFieldPreservesCommentAfterExplicitDocumentMarker's sibling case
// in patch.go's hasMultipleDocuments doc comment), so nothing is at risk.
func TestPatchFieldAllowsTrailingDecorativeSeparator(t *testing.T) {
	cases := map[string]string{
		"bare":                "version: 1\nprovider: mock\n---\n",
		"trailing blank line": "version: 1\nprovider: mock\n---\n\n",
		"dots terminator":     "version: 1\nprovider: mock\n---\n...\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "config.yml", body)
			if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
				t.Fatalf("PatchField: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "lang: tr") {
				t.Errorf("output.lang was not set; file:\n%s", got)
			}
			if !strings.Contains(string(got), "provider: mock") {
				t.Errorf("existing provider key lost; file:\n%s", got)
			}
		})
	}
}

// A SECOND document with real content (not just a decorative trailing
// separator) must still be refused — item H narrows the false positive, it
// must not reopen item B.
func TestPatchFieldStillRefusesRealSecondDocument(t *testing.T) {
	const body = "version: 1\nprovider: mock\n---\nb: 2\n"
	path := writeConfig(t, "config.yml", body)

	err := PatchField(path, []string{"output", "lang"}, "tr")
	if err == nil {
		t.Fatal("want an error: the second document has real content (b: 2), not nothing")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("file must be left untouched on refusal.\nbefore:\n%s\nafter:\n%s", body, after)
	}
}

// Item C [MINOR]: a document that starts with an explicit `---` separator
// followed by nothing but a comment keeps that comment through a patch —
// yaml.v3 attaches it to the document node itself (Kind stays
// DocumentNode, not the zero value), so this case never takes the
// "nothing parsed, start fresh" fallback in parseOrNewDocument.
func TestPatchFieldPreservesCommentAfterExplicitDocumentMarker(t *testing.T) {
	const body = "---\n# just a comment\n"
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# just a comment") {
		t.Errorf("comment after the `---` marker was dropped; file:\n%s", got)
	}
	if !strings.Contains(string(got), "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", got)
	}
}

// Item G [was C, still open after turu 1]: a plain-comment file with NO
// `---` marker (e.g. a user comments out every line to try the defaults,
// including a disabled api_key) has nothing yaml.v3's parser attaches
// anywhere recoverable — Kind stays the Go zero value and, empirically, so
// does every other field including comments. PatchField can't tell "just
// blank lines, nothing lost" apart from "a commented-out api_key, real
// loss" from what the parser hands back, so it must REFUSE rather than
// silently start fresh — the same policy as a multi-document file. The
// turu-1 fix checked doc.Content instead, which is unreachable (Content is
// never populated when Kind is 0 either), so it changed nothing; this test
// asserts the actual outcome (an error, file untouched), not just that
// *some* string is present in the result, which is what let the
// unreachable guard through review the first time.
func TestPatchFieldRefusesPureCommentFileWithNoDocumentMarker(t *testing.T) {
	const body = "# version: 1\n# provider: mock\n# api_key: sk-keepme\n"
	path := writeConfig(t, "config.yml", body)

	err := PatchField(path, []string{"output", "lang"}, "tr")
	if err == nil {
		t.Fatal("want an error for a file with nothing yaml.v3 can recover, got nil")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("file must be left untouched on refusal.\nbefore:\n%s\nafter:\n%s", body, after)
	}
}

// A whitespace-only file (no comments at all) is the same case by the same
// reasoning — refused, not silently replaced with an empty document.
func TestPatchFieldRefusesWhitespaceOnlyFile(t *testing.T) {
	path := writeConfig(t, "config.yml", "\n\n\n")

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err == nil {
		t.Fatal("want an error for a whitespace-only existing file, got nil")
	}
}

// Item D [nit→fixed]: a merge key (`<<:`) must not grow an explicit
// "!!merge" tag annotation it didn't have in the source — the one visible
// reformatting the package doesn't accept. Functionally harmless either
// way (both parse identically), but the acceptance criteria call for
// "byte-identical apart from the changed key", and this was the one
// visible gap.
func TestPatchFieldDoesNotAddExplicitMergeTag(t *testing.T) {
	const body = `version: 1
provider: mock
x-defaults: &d
  ttl_days: 3
cache:
  <<: *d
  enabled: true
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if strings.Contains(out, "!!merge") {
		t.Errorf("merge key grew an explicit !!merge tag it didn't have; file:\n%s", out)
	}
	if !strings.Contains(out, "<<: *d") {
		t.Errorf("merge key itself was lost; file:\n%s", out)
	}

	var cfg Config
	if err := yaml.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("patched file with a bare merge key failed to parse: %v\nfile:\n%s", err, out)
	}
	if cfg.Cache.TTLDays != 3 {
		t.Errorf("cache.ttl_days (via merge) = %d, want 3 — merge must still resolve", cfg.Cache.TTLDays)
	}
}

// Item I [turu 2]: clearMergeTags must only touch a key whose VALUE is the
// literal "<<" — checking the "!!merge" tag alone is too broad, since
// nothing stops a key spelled some other way from also resolving to that
// tag. Untagging a key that isn't actually a merge key by yaml.v3's own
// resolution rule (keyed off the value, not the tag) would be a
// gratuitous, undocumented rewrite of content this package has no business
// touching.
func TestPatchFieldDoesNotUntagNonMergeKeys(t *testing.T) {
	const body = "version: 1\nprovider: mock\nx-weird:\n  !!merge foo: 1\n"
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "!!merge foo: 1") {
		t.Errorf("a non-\"<<\" key explicitly tagged !!merge must keep its tag untouched; file:\n%s", out)
	}
}

// Item E: accepted, out-of-scope reformatting — pinned so it stays
// deliberate rather than an unnoticed regression. None of these lose data;
// see PatchField's package-level doc comment for the reasoning.

// Item L [turu 2]: asserting only the absence of "\n\n" also passes if
// PatchField wrote an empty/garbage file — this pins the actual accepted
// behavior (reformatted, but every key still there with its right value),
// not just one symptom of it.
func TestPatchFieldAcceptedLimit_BlankLinesCollapse(t *testing.T) {
	const body = "version: 1\n\nprovider: mock\n"
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if strings.Contains(out, "\n\n") {
		t.Errorf("accepted-limit test is stale: blank line survived (that would be GOOD news, update the doc comment); file:\n%q", got)
	}
	if !strings.Contains(out, "provider: mock") {
		t.Errorf("existing provider key lost, not just reformatted; file:\n%s", out)
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

// Item L [turu 2]: same reasoning — absence of "\r\n" alone would also pass
// for an empty/garbage file.
func TestPatchFieldAcceptedLimit_CRLFBecomesLF(t *testing.T) {
	const body = "version: 1\r\nprovider: mock\r\n"
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if strings.Contains(out, "\r\n") {
		t.Errorf("accepted-limit test is stale: CRLF survived (that would be GOOD news, update the doc comment); file:\n%q", got)
	}
	if !strings.Contains(out, "provider: mock") {
		t.Errorf("existing provider key lost, not just reformatted; file:\n%s", out)
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

func TestPatchFieldAcceptedLimit_ZeroIndentSequenceGetsReindented(t *testing.T) {
	const body = `version: 1
provider: mock
guard:
  secret_patterns:
  - name: Acme Internal Token
    regex: 'acme_[A-Za-z0-9]{32}'
`
	path := writeConfig(t, "config.yml", body)

	if err := PatchField(path, []string{"output", "lang"}, "tr"); err != nil {
		t.Fatalf("PatchField: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if strings.Contains(out, "\n  secret_patterns:\n  - name:") {
		t.Errorf("accepted-limit test is stale: zero-indent sequence survived (that would be GOOD news, update the doc comment); file:\n%s", out)
	}
	// Not a loss: the pattern itself must still be there, just re-indented.
	if !strings.Contains(out, "name: Acme Internal Token") {
		t.Errorf("sequence content was lost, not just re-indented; file:\n%s", out)
	}
}
