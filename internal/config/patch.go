// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PatchField writes a single value into the YAML document at path, touching
// only the node named by keyPath.
//
// It exists to replace the decode-into-Config-then-marshal-the-whole-thing
// write path `config set` and `providers use` used before: that round-trip
// silently destroyed three different things on a partial or hand-edited
// file — see .ssot/plans/2026-09-19-wave-0-ground-truth/06-config-set-write-path.md
// for the full account. A `Default()`-style merge would only have fixed the
// first of those (zero-value destruction); an unknown top-level `x-` key has
// no field in Config at all, so no merge strategy can carry it through a
// struct round-trip — only operating on the raw document does.
//
// # What is guaranteed
//
// Everything else in the document is preserved: comments, key order,
// anchors/aliases (e.g. a top-level `x-defaults: &d` merged elsewhere via
// `<<: *d`, or a scalar shared via `&m`/`*m` — patching the anchor's own
// node keeps every alias to it pointing at the updated value, same as a
// human editing the `&m` line would), and any key the typed Config schema
// doesn't know about (including one dropped by --ignore-unknown-config).
//
// Two shapes are refused outright rather than risking a silent loss:
//
//   - a multi-document file (a second `---`-separated document with real
//     content) — only the first document would round-trip;
//   - a non-empty file yaml.v3 attaches nothing recoverable to at all (every
//     line commented out, or whitespace-only, with no `---` marker) —
//     there is no way to tell "just blank lines" apart from "a commented-out
//     api_key" from what the parser hands back, so this refuses rather than
//     guess (Faz 06 review turu 2, item G).
//
// # What is NOT guaranteed (accepted, by design — Faz 06 review turu 1 item E, turu 2 items I, J, K)
//
// Re-emitting through yaml.v3's encoder is not a line-level surgical patch.
// None of the following lose data, and each is pinned by a test
// (patch_test.go) so the behavior stays deliberate rather than drifting:
//
//   - blank lines between sections are collapsed;
//   - CRLF line endings become LF;
//   - a zero-indented block sequence (`key:\n- item`) is re-indented under
//     its key (`key:\n  - item`);
//   - the exact scalar node being patched loses its own prior quote style
//     and any explicit tag (`model: "sonnet"` → `model: opus`, dropping the
//     quotes; `base_url: !!str http://x` → `base_url: http://y`, dropping
//     the tag) — everything else in the document, flow-style collections
//     included, keeps its original style;
//   - an inline comment's spacing before `#` is normalized to a single
//     space on ANY line in the document, not just the one being patched
//     (`provider: mock  # note` → `provider: mock # note`) — this is
//     yaml.v3's encoder, not something PatchField's own logic touches; the
//     comment's content and every other line are untouched, nothing is lost;
//   - a merge key (`<<:`) that happens to be the ONLY nested block-mapping
//     structure under an unknown/`x-` key can still push detectIndent to
//     its 4-space fallback instead of matching the file's real indent
//     width — unreachable through any schema-known key, since
//     `guard.secret_patterns` and friends are always nested under a real
//     mapping detectIndent already finds first.
//
// Fixing any of these would require a real line-level patch instead of a
// parse/mutate/re-emit round-trip, which is out of scope for this fix.
//
// keyPath is the dotted-path segments (e.g. ["output", "lang"] or
// ["providers", "anthropic", "api_key"]); every segment but the last names a
// mapping to descend into (created empty if absent, or converted in place if
// it was a bare/null key). value's Go type determines the YAML scalar tag
// written (bool/int/float64/string): pass the already-coerced,
// already-validated value, not the raw command-line string.
//
// When path does not exist yet, or is a genuinely empty (zero-byte) file,
// PatchField creates it with just the minimal nested structure for keyPath
// — there is nothing on disk to preserve, so there is nothing lost by not
// writing a full default skeleton here; callers that want that first-run
// skeleton (setup.WriteConfig with a config.Default()-based value) should
// check existence themselves and skip PatchField for the "file doesn't
// exist" case, exactly as internal/cli/config.go and
// internal/cli/providers.go do.
func PatchField(path string, keyPath []string, value any) error {
	if path == "" {
		return errors.New("config: PatchField: empty path")
	}
	if len(keyPath) == 0 {
		return errors.New("config: PatchField: empty key path")
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	// A YAML stream can hold more than one `---`-separated document.
	// yaml.Unmarshal(data, &node) silently decodes only the first and
	// reports no error either way, so parseOrNewDocument below can't tell
	// the difference — a config.Load of the same file today only ever
	// looks at the first document too, so it's a WORKING file with real
	// content the user would lose. Re-encoding just the first document
	// would delete the rest with no warning (Faz 06 review turu 1, item
	// B) — cheaper and safer to refuse the write outright than to try to
	// round-trip every document.
	if multi, err := hasMultipleDocuments(data); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	} else if multi {
		return fmt.Errorf("config: %s contains more than one YAML document (a `---` separator); refusing to write since only the first document would be preserved — edit the file directly instead", path)
	}

	doc, err := parseOrNewDocument(data)
	if err != nil {
		if errors.Is(err, errNothingParsed) {
			return fmt.Errorf("config: %s has content yaml.v3 cannot recover after a patch (entirely blank, or every line commented out, with no `---` document marker); refusing to write since a comment there — an api_key someone commented out to disable, for instance — has nowhere to survive a re-encode. Edit the file directly, or run `commitbrief setup` to rewrite it from scratch", path)
		}
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	root, err := documentMapping(doc)
	if err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}

	node := root
	for _, key := range keyPath[:len(keyPath)-1] {
		node, err = mappingChild(node, key)
		if err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
	}
	if err := setScalarField(node, keyPath[len(keyPath)-1], value); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}

	// yaml.v3 resolves a bare `<<:` merge key's tag to the explicit
	// "!!merge" during parsing and, left alone, prints that tag back out
	// explicitly on the next encode (`!!merge <<: *d`) even though the
	// source never wrote it — the one visible reformatting this package
	// doesn't accept (Faz 06 review turu 1, item D). Clearing the tag back
	// to implicit before encoding is cosmetic only: merge resolution is
	// driven by the literal key value "<<", not by whether its tag was
	// explicit in the source, so this doesn't change what the file means,
	// only what it looks like.
	clearMergeTags(doc)

	// yaml.v3's Node round-trip preserves comments, key order and
	// anchors/aliases, but NOT the original indent width — that's an
	// encoder setting, not something a Node stores. Left at the library
	// default (4 spaces) it would silently reformat a hand-edited 2-space
	// file's untouched sections on every `config set`. detectIndent reads
	// the width the file already uses (from the parsed nodes' Column
	// positions) so an existing 2-space file stays 2-space.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndent(root))
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("config: marshal %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: marshal %s: %w", path, err)
	}
	return atomicWriteFile(path, buf.Bytes())
}

// hasMultipleDocuments reports whether data contains more than one
// `---`-separated YAML document. It decodes documents one at a time (never
// materializing more than two Nodes) purely to count them, discarding
// their content — the real parse happens separately in
// parseOrNewDocument.
func hasMultipleDocuments(data []byte) (bool, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var first yaml.Node
	if err := dec.Decode(&first); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	var second yaml.Node
	if err := dec.Decode(&second); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	// A trailing `---` with nothing meaningful after it (end of file, only
	// blank lines, or a `...` terminator) decodes to an empty second
	// document — real for the parser, but not a second document a human
	// authored. Refusing to write over this is pure friction: not one byte
	// would be lost, and any comment that visually sits after that last
	// `---` is actually attached by yaml.v3 to the FIRST document's
	// FootComment (verified empirically; there's nothing on the empty
	// second document itself), which PatchField already preserves
	// untouched — so there is nothing left for this "document" to hold
	// (Faz 06 review turu 2, item H). A second document with REAL content
	// (a mapping, a non-null scalar, or one that carries its own comment)
	// still counts.
	if isEmptyTrailingDocument(&second) {
		return false, nil
	}
	return true, nil
}

// isEmptyTrailingDocument reports whether doc is what a bare trailing
// `---` (nothing, whitespace, or `...` after it) decodes to: no comments of
// its own, and either no content at all or a single null scalar with no
// comments of its own either.
func isEmptyTrailingDocument(doc *yaml.Node) bool {
	if doc.HeadComment != "" || doc.LineComment != "" || doc.FootComment != "" {
		return false
	}
	if len(doc.Content) == 0 {
		return true
	}
	if len(doc.Content) != 1 {
		return false
	}
	c := doc.Content[0]
	return c.Kind == yaml.ScalarNode && c.Tag == "!!null" &&
		c.HeadComment == "" && c.LineComment == "" && c.FootComment == ""
}

// detectIndent returns the indent width (in spaces) the document already
// uses, inferred from the column of the first nested block-style mapping
// key found under root. Falls back to yaml.v3's own default (4) when root
// has no nested mapping yet (a flat or brand-new file) or the width can't
// be determined.
func detectIndent(root *yaml.Node) int {
	const fallback = 4
	if root.Kind != yaml.MappingNode || len(root.Content) == 0 {
		return fallback
	}
	baseCol := root.Content[0].Column
	if baseCol <= 0 {
		return fallback
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		v := root.Content[i+1]
		if v.Kind != yaml.MappingNode || v.Style == yaml.FlowStyle || len(v.Content) == 0 {
			continue
		}
		if width := v.Content[0].Column - baseCol; width > 0 {
			return width
		}
	}
	return fallback
}

// errNothingParsed is parseOrNewDocument's sentinel for "data is non-empty
// but yaml.v3 didn't attach it anywhere recoverable" — see its doc comment.
// PatchField turns it into a refusal-to-write error, the same policy as a
// multi-document file (hasMultipleDocuments): refuse rather than silently
// destroy whatever was in there.
var errNothingParsed = errors.New("config: nothing parsed")

// parseOrNewDocument parses data as a YAML document node, or returns a
// fresh empty-mapping document when data is empty (a brand new file — there
// is nothing on disk to preserve, so nothing is lost by starting fresh).
//
// A non-empty file that decodes to Kind == 0 (a whitespace-only file, or a
// plain-comment one with no `---` marker — e.g. a user comments out their
// whole config to try the defaults) is different: yaml.v3 never populates
// doc at all for it, Kind stays the Go zero value, and empirically
// (yaml.v3 v3.0.1) so does everything else on it, comments included — a
// commented-out `api_key` a user disabled this way has nowhere to land
// after a re-encode (Faz 06 review turu 2, item G; an earlier attempt at
// this guard checked doc.Content instead of returning errNothingParsed,
// which is unreachable — Content is never populated when Kind is 0 either,
// so that check silently discarded the file exactly as before; see the
// reverted-guard mutation check in patch_test.go). Since there is no way to
// tell "just blank lines, nothing lost" apart from "meaningful comments,
// something lost" from what the parser hands back, this returns
// errNothingParsed unconditionally for that case and lets the caller
// refuse the write — the same "refuse rather than destroy" policy as a
// multi-document file, not a silent fresh start.
//
// A document that starts with an explicit `---` marker does NOT hit this
// path even when everything after it is comments-only: yaml.v3 gives that
// case Kind == DocumentNode (not 0), with the comment retained on the
// document's own FootComment — see
// TestPatchFieldPreservesCommentAfterExplicitDocumentMarker.
func parseOrNewDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return newEmptyDocument(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		return nil, errNothingParsed
	}
	return &doc, nil
}

// newEmptyDocument is the minimal document PatchField grows a nested
// structure into when there's nothing on disk (or nothing recoverable) to
// preserve.
func newEmptyDocument() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

// documentMapping returns the top-level mapping node of doc, converting a
// null top-level value (an entirely empty document parses to a null
// scalar) into an empty mapping in place.
func documentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Value = ""
		root.Content = nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("top-level YAML value is not a mapping")
	}
	return root, nil
}

// mappingChild returns the mapping node for key within parent, creating an
// empty mapping under a new key if key isn't present yet. An existing key
// whose value is null is converted into a mapping in place (a bare `output:`
// with no children, or a key added by hand with no value yet); any other
// existing non-mapping value is a type conflict the caller can't safely
// grow a nested structure into.
func mappingChild(parent *yaml.Node, key string) (*yaml.Node, error) {
	if idx := mappingKeyIndex(parent, key); idx >= 0 {
		v := parent.Content[idx+1]
		switch {
		case v.Kind == yaml.MappingNode:
			return v, nil
		case v.Kind == yaml.ScalarNode && v.Tag == "!!null":
			v.Kind = yaml.MappingNode
			v.Tag = "!!map"
			v.Value = ""
			v.Content = nil
			return v, nil
		default:
			return nil, fmt.Errorf("%q is not a mapping", key)
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode, nil
}

// setScalarField sets key's value within parent, appending a new key/value
// pair when key doesn't exist yet. When it does, only the value node's
// kind/tag/value/content are replaced (via Node.Encode, which does
// `*n = *freshlyEncodedNode` internally — a FULL struct overwrite, not a
// field-by-field update) — its comments AND its anchor are saved and
// restored around that call so both survive.
//
// The anchor save/restore is load-bearing, not cosmetic: an alias (`*m`)
// elsewhere in the document holds a direct pointer to this same Node value
// (see yaml.Node.Alias's doc comment), not a copy or a name lookup. Encode
// clearing v.Anchor without it being restored doesn't just drop a
// decoration — every `*m` alias in the file instantly dangles, and
// dangling aliases fail to parse at all (exit 0 on write, then every next
// command — `config show` included — errors "unknown anchor"; see Faz 06
// review turu 1, item A). A patched anchor's aliases then track its new
// value, same as if a person had hand-edited the `&m` line themselves —
// that's the correct, expected propagation, not a side effect to suppress.
func setScalarField(parent *yaml.Node, key string, value any) error {
	if idx := mappingKeyIndex(parent, key); idx >= 0 {
		v := parent.Content[idx+1]
		head, line, foot := v.HeadComment, v.LineComment, v.FootComment
		anchor := v.Anchor
		if err := v.Encode(value); err != nil {
			return fmt.Errorf("encode %q: %w", key, err)
		}
		v.HeadComment, v.LineComment, v.FootComment = head, line, foot
		v.Anchor = anchor
		return nil
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{}
	if err := valNode.Encode(value); err != nil {
		return fmt.Errorf("encode %q: %w", key, err)
	}
	parent.Content = append(parent.Content, keyNode, valNode)
	return nil
}

// clearMergeTags walks the whole tree under n and blanks the Tag of any
// `<<:` merge key (Value == "<<", resolved by the parser to Tag ==
// "!!merge"), so the encoder prints it back the way it was almost
// certainly authored — bare, without an explicit "!!merge" annotation. See
// PatchField's call site for why this is safe: it only affects how the key
// is printed, not what it means. Both the tag AND the literal "<<" value
// are checked (Faz 06 review turu 2, item I) — a key that only carries the
// explicit tag without the "<<" spelling isn't a merge key by yaml.v3's own
// resolution rules (which key off the value, not the tag) and untagging it
// would be a gratuitous, undocumented rewrite of user content.
func clearMergeTags(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag == "!!merge" && k.Value == "<<" {
				k.Tag = ""
			}
			clearMergeTags(n.Content[i+1])
		}
		return
	}
	for _, c := range n.Content {
		clearMergeTags(c)
	}
}

// mappingKeyIndex returns the Content index of key's key-node within a
// mapping node (so the value is at index+1), or -1 if not present.
func mappingKeyIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return i
		}
	}
	return -1
}

// atomicWriteFile mirrors setup.WriteConfig's write discipline (parent dir
// at 0700 since config may hold API keys, temp file + rename so a crash
// mid-write never leaves a half-written config.yml) without importing
// internal/setup, which would create an import cycle (setup already imports
// config).
func atomicWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
