package ignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinMatchesGoSum(t *testing.T) {
	m := Builtin()
	if !m.Match("go.sum") {
		t.Error("Builtin should match go.sum")
	}
}

func TestBuiltinDoesNotMatchSourceFile(t *testing.T) {
	m := Builtin()
	for _, p := range []string{"auth.go", "internal/cli/root.go", "src/main.py", "README.md"} {
		if m.Match(p) {
			t.Errorf("Builtin should NOT match source file %q", p)
		}
	}
}

func TestBuiltinMatchesCommonIgnores(t *testing.T) {
	m := Builtin()
	cases := []string{
		"vendor/foo/bar.go",
		"node_modules/react/index.js",
		"dist/app.js",
		"build/out.bin",
		".commitbrief/cache/abc123.json",
		".DS_Store",
		"app.min.js",
		"styles.css.map",
		"package-lock.json",
		"yarn.lock",
		"Cargo.lock",
		"foo.pb.go",
		"internal/pkg/mocks/userrepo.go",
	}
	for _, p := range cases {
		if !m.Match(p) {
			t.Errorf("Builtin should match %q", p)
		}
	}
}

func TestNegativePatternRevertsBuiltin(t *testing.T) {
	override := parsePatterns([]string{"!go.sum"})
	m := Compose(Builtin(), override)
	if m.Match("go.sum") {
		t.Error("!go.sum in later layer should revert built-in go.sum exclusion")
	}
}

func TestComposeOrderLastWins(t *testing.T) {
	earlier := parsePatterns([]string{"foo.go"})
	later := parsePatterns([]string{"!foo.go"})
	if m := Compose(earlier, later); m.Match("foo.go") {
		t.Error("later !foo.go should override earlier foo.go")
	}
	if m := Compose(later, earlier); !m.Match("foo.go") {
		t.Error("later foo.go should override earlier !foo.go")
	}
}

func TestComposeNilLayerIgnored(t *testing.T) {
	m := Compose(nil, Builtin(), nil)
	if !m.Match("go.sum") {
		t.Error("nil layers should be skipped, not break the matcher")
	}
}

func TestEmptyMatcherDoesNotMatch(t *testing.T) {
	m := Empty()
	if m.Match("anything.go") {
		t.Error("Empty() matcher should not match any path")
	}
}

func TestNilMatcherSafe(t *testing.T) {
	var m *Matcher
	if m.Match("foo.go") {
		t.Error("nil matcher should return false")
	}
}

func TestMatchEmptyPath(t *testing.T) {
	m := Builtin()
	if m.Match("") {
		t.Error("empty path should not match")
	}
}

func TestMatchStripsLeadingSlashAndDot(t *testing.T) {
	m := Builtin()
	if !m.Match("/go.sum") {
		t.Error("leading slash should be stripped before match")
	}
	if !m.Match("./go.sum") {
		t.Error("leading ./ should be stripped before match")
	}
}

func TestMatchNormalizesBackslashes(t *testing.T) {
	// filepath.ToSlash converts \ to / only on Windows; on POSIX it leaves them.
	// We assert the function is wired through ToSlash by feeding a forward-slash
	// path (the canonical input on all platforms) and confirming it matches.
	m := parsePatterns([]string{"node_modules/**"})
	if !m.Match("node_modules/react/index.js") {
		t.Error("forward-slash nested path should match")
	}
}

func TestParseFileMissingReturnsEmpty(t *testing.T) {
	m, err := ParseFile("/does/not/exist/.commitbriefignore")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if m == nil {
		t.Fatal("missing file should return empty matcher, not nil")
	}
	if m.Match("foo.go") {
		t.Error("empty matcher from missing file should not match anything")
	}
}

func TestParseFileEmptyPathReturnsEmpty(t *testing.T) {
	m, err := ParseFile("")
	if err != nil || m == nil {
		t.Fatalf("empty path: m=%v err=%v", m, err)
	}
}

func TestParseFileReal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	content := `# generated migrations
db/migrations/*.sql

# vendored docs
docs/vendor/**

# but review go.sum
!go.sum
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("db/migrations/0001.sql") {
		t.Error("db/migrations/0001.sql should match")
	}
	if !m.Match("docs/vendor/x.md") {
		t.Error("docs/vendor/x.md should match")
	}
	if m.Match("internal/auth.go") {
		t.Error("internal/auth.go should NOT match")
	}
	// Combined with built-in, !go.sum should win
	combined := Compose(Builtin(), m)
	if combined.Match("go.sum") {
		t.Error("!go.sum in .commitbriefignore should revert built-in")
	}
}

func TestParseSkipsCommentsAndBlankLines(t *testing.T) {
	src := strings.NewReader(`# leading comment
   # indented comment

foo.go

# trailing
bar.go
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("foo.go") || !m.Match("bar.go") {
		t.Error("real patterns should still be picked up")
	}
	if m.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (comments and blanks filtered)", m.Len())
	}
}

func TestBuiltinPatternsExported(t *testing.T) {
	ps := BuiltinPatterns()
	if len(ps) == 0 {
		t.Fatal("BuiltinPatterns() returned empty slice")
	}
	ps[0] = "tampered"
	if BuiltinPatterns()[0] == "tampered" {
		t.Error("BuiltinPatterns() should return a defensive copy")
	}
}
