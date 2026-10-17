// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

// The fixtures below are verbatim `git diff -U3` output (exec git, default
// funcname rule) so the tests exercise exactly what a real review parses.

// symbolFixtureModify: one hunk inside Handle; git puts Handle's signature in
// the hunk header (the body has no qualifying line).
const symbolFixtureModify = `diff --git a/svc.go b/svc.go
index 97f422f..393ef51 100644
--- a/svc.go
+++ b/svc.go
@@ -42,7 +42,7 @@ func (s *Server) Handle(ctx context.Context, req Request) (int, error) {
 	v27 := 27
 	v28 := 28
 	v29 := 29
-	query := "SELECT * FROM t WHERE id=" + req.ID
+	query := "SELECT * FROM t WHERE id=" + req.ID + " LIMIT 1"
 	return run(query)
 }
 
`

// symbolFixtureShifted: same edit as symbolFixtureModify plus an unrelated
// line added to Alpha, above the Handle hunk, so Handle's hunk moves down one line.
const symbolFixtureShifted = `diff --git a/svc.go b/svc.go
index 97f422f..e524cbf 100644
--- a/svc.go
+++ b/svc.go
@@ -5,6 +5,7 @@ import "fmt"
 // helper
 func Alpha(a int) int {
 	x := a + 1
+	_ = fmt.Sprint(x)
 	y := x * 2
 	z := y - 3
 	w := z + 4
@@ -42,7 +43,7 @@ func (s *Server) Handle(ctx context.Context, req Request) (int, error) {
 	v27 := 27
 	v28 := 28
 	v29 := 29
-	query := "SELECT * FROM t WHERE id=" + req.ID
+	query := "SELECT * FROM t WHERE id=" + req.ID + " LIMIT 1"
 	return run(query)
 }
 
`

// symbolFixtureNewFunc: a new function appended after Handle.
const symbolFixtureNewFunc = `diff --git a/svc.go b/svc.go
index 97f422f..6aa3c99 100644
--- a/svc.go
+++ b/svc.go
@@ -46,3 +46,7 @@ func (s *Server) Handle(ctx context.Context, req Request) (int, error) {
 	return run(query)
 }
 
+func NewThing(x int, y string) (int, error) {
+	z := x * 2
+	return z, nil
+}
`

// symbolFixtureIndented: a diff driver whose funcname regexp keeps leading
// whitespace, so the raw header is `@@ -11,5 +11,5 @@     def run(self):`.
const symbolFixtureIndented = `diff --git a/run.py b/run.py
index 795e919..d42a6a3 100644
--- a/run.py
+++ b/run.py
@@ -11,5 +11,5 @@     def run(self):
         v8 = 8
         v9 = 9
         v10 = 10
-        v11 = 11
+        v11 = 12
         return v0
`

// symbolFixtureLong: the funcname line is longer than 80 bytes; git cuts the
// header text at 80 bytes.
const symbolFixtureLong = `diff --git a/long.go b/long.go
index 91d940c..1f6fdb1 100644
--- a/long.go
+++ b/long.go
@@ -29,7 +29,7 @@ func (s *VeryLongServerNameForTheSakeOfTesting) ProcessIncomingRequestWithAVeryL
 	v21 := 21
 	v22 := 22
 	v23 := 23
-	v24 := 24
+	v24 := 25
 	return nil, nil
 }
 
`

func mustParse(t *testing.T, raw string) Diff {
	t.Helper()
	d, err := Parse(git.Diff{Content: raw, Origin: git.OriginDiff})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestParseHunkHeaderRaw(t *testing.T) {
	cases := []struct {
		line, header, raw string
	}{
		{"@@ -1,3 +1,4 @@ func Foo() {", "func Foo() {", "func Foo() {"},
		{"@@ -1,3 +1,4 @@     def run(self):", "def run(self):", "    def run(self):"},
		{"@@ -1,3 +1,4 @@", "", ""},
		{"@@ -1,3 +1,4 @@ trailing\r", "trailing", "trailing"},
		{"@@ -1,3 +1,4 @@\tfoo", "foo", "\tfoo"},
	}
	for _, tc := range cases {
		h, err := parseHunkHeader(tc.line)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		if h.Header != tc.header || h.HeaderRaw != tc.raw {
			t.Errorf("%q: Header=%q HeaderRaw=%q, want %q / %q", tc.line, h.Header, h.HeaderRaw, tc.header, tc.raw)
		}
	}
}

func TestSymbolForHunkHeaderFallback(t *testing.T) {
	d := mustParse(t, symbolFixtureModify)
	got := d.SymbolFor("svc.go", 45)
	if want := "func () Handle() ()"; got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
}

func TestSymbolForStableWhenUnrelatedLineAddedAbove(t *testing.T) {
	a := mustParse(t, symbolFixtureModify).SymbolFor("svc.go", 45)
	b := mustParse(t, symbolFixtureShifted).SymbolFor("svc.go", 46)
	if a == "" || a != b {
		t.Errorf("symbol drifted: before=%q after=%q", a, b)
	}
}

func TestSymbolForNewFunctionInsideHunk(t *testing.T) {
	d := mustParse(t, symbolFixtureNewFunc)
	if got, want := d.SymbolFor("svc.go", 50), "func NewThing() ()"; got != want {
		t.Errorf("inside new func: symbol = %q, want %q", got, want)
	}
	// A line above the new function still belongs to Handle (hunk header).
	if got, want := d.SymbolFor("svc.go", 47), "func () Handle() ()"; got != want {
		t.Errorf("above new func: symbol = %q, want %q", got, want)
	}
}

func TestSymbolForIndentedRawHeaderIsNotASymbol(t *testing.T) {
	d := mustParse(t, symbolFixtureIndented)
	h := d.Files[0].Hunks[0]
	if h.HeaderRaw != "    def run(self):" {
		t.Fatalf("HeaderRaw = %q (fixture no longer indented)", h.HeaderRaw)
	}
	if h.Header != "def run(self):" {
		t.Fatalf("Header = %q, want the trimmed form", h.Header)
	}
	if got := d.SymbolFor("run.py", 14); got != "" {
		t.Errorf("symbol = %q, want empty (indented header must not qualify)", got)
	}
}

func TestSymbolForCutsAtGitsEightyBytes(t *testing.T) {
	d := mustParse(t, symbolFixtureLong)
	h := d.Files[0].Hunks[0]
	if len(h.HeaderRaw) != 80 {
		t.Fatalf("git header length = %d, want 80 (fixture changed)", len(h.HeaderRaw))
	}
	got := d.SymbolFor("long.go", 32)
	want := "func () ProcessIncomingRequestWithAVeryL"
	if got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
	// The same signature seen in full inside a hunk body is cut at the same
	// place, so header-sourced and body-sourced symbols agree.
	full := "func (s *VeryLongServerNameForTheSakeOfTesting) ProcessIncomingRequestWithAVeryLongName(ctx context.Context, request IncomingRequest, opts ...Option) (Response, error) {"
	if g := normaliseSymbol(full); g != want {
		t.Errorf("normaliseSymbol(full) = %q, want %q", g, want)
	}
}

func TestSymbolForBodyLineBeatsHeader(t *testing.T) {
	raw := "diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n" +
		"@@ -10,5 +10,6 @@ func Outer() {\n" +
		" \tx := 1\n" +
		" func Inner(a int) {\n" +
		" \ty := 2\n" +
		"+\tz := 3\n" +
		" }\n"
	d := mustParse(t, raw)
	if got, want := d.SymbolFor("m.go", 13), "func Inner()"; got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
	// Before Inner starts the header still wins.
	if got, want := d.SymbolFor("m.go", 10), "func Outer()"; got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
}

func TestSymbolForLeftSideAndMisses(t *testing.T) {
	raw := "diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n" +
		"@@ -20,4 +20,2 @@ func Gone() {\n" +
		" \ta := 1\n" +
		"-\tb := 2\n" +
		"-\tc := 3\n" +
		" }\n"
	d := mustParse(t, raw)
	// Line 22 exists only on the old side (a removed line).
	if got, want := d.SymbolFor("m.go", 22), "func Gone()"; got != want {
		t.Errorf("left side: symbol = %q, want %q", got, want)
	}
	for name, tc := range map[string]struct {
		file string
		line int
	}{
		"line zero":     {"m.go", 0},
		"negative line": {"m.go", -3},
		"unknown file":  {"other.go", 21},
		"outside hunk":  {"m.go", 99},
	} {
		if got := d.SymbolFor(tc.file, tc.line); got != "" {
			t.Errorf("%s: symbol = %q, want empty", name, got)
		}
	}
	if got, want := d.SymbolFor("./m.go", 21), "func Gone()"; got != want {
		t.Errorf("leading ./: symbol = %q, want %q", got, want)
	}
}

func TestSymbolForFirstSectionWinsOnDuplicatePath(t *testing.T) {
	raw := "diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n" +
		"@@ -1,2 +1,2 @@ func First() {\n \ta\n+\tb\n" +
		"diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n" +
		"@@ -1,2 +1,2 @@ func Second() {\n \ta\n+\tb\n"
	d := mustParse(t, raw)
	if got, want := d.SymbolFor("m.go", 2), "func First()"; got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
}

func TestNormaliseSymbol(t *testing.T) {
	cases := []struct{ in, want string }{
		{"func (s *Server) Handle(ctx context.Context) (int, error) {", "func () Handle() ()"},
		{"public function handle(Request $r): Response", "public function handle(): Response"},
		{"def run(self):", "def run()"},
		{"func   Foo(a,\tb)   {  \r", "func Foo()"},
		{"func Nested(a func(int) (int, error), b int) error {", "func Nested() error"},
		{"func Cut(a int, b string", "func Cut"},
		{"class Foo extends Bar {", "class Foo extends Bar"},
		{"$x = foo(1) + bar(2)", "$x = foo() + bar()"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normaliseSymbol(tc.in); got != tc.want {
			t.Errorf("normaliseSymbol(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormaliseSymbolNeverSplitsUTF8(t *testing.T) {
	// 78 ASCII bytes then a 3-byte rune straddling the 80-byte cap.
	in := "x" + strings.Repeat("a", 77) + "\u20ac" + "tail"
	got := normaliseSymbol(in)
	if len(got) > 80 {
		t.Errorf("len = %d, want <= 80", len(got))
	}
	if strings.ContainsRune(got, '\uFFFD') || got != "x"+strings.Repeat("a", 77) {
		t.Errorf("got %q, want the ASCII prefix only", got)
	}
}

func TestNormaliseSymbolDropsPartialRuneAtExactlyEightyBytes(t *testing.T) {
	// git cuts funcname at 80 bytes without looking at UTF-8, so the raw
	// header can be exactly 80 bytes and end in the first 2 bytes of a rune.
	in := "x" + strings.Repeat("a", 77) + "\u20ac"[:2]
	if len(in) != 80 {
		t.Fatalf("fixture length = %d, want 80", len(in))
	}
	if got, want := normaliseSymbol(in), "x"+strings.Repeat("a", 77); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSymbolForHeaderPathDropsPartialRune(t *testing.T) {
	hdr := "x" + strings.Repeat("a", 77) + "\u20ac"[:2]
	d := Diff{Files: []FileDiff{{Path: "p.go", Hunks: []Hunk{{
		NewStart: 10, NewLines: 3, HeaderRaw: hdr,
		Lines: []HunkLine{{Kind: LineContext, Text: "\tbody"}},
	}}}}}
	if got, want := d.SymbolFor("p.go", 10), "x"+strings.Repeat("a", 77); got != want {
		t.Errorf("symbol = %q, want %q", got, want)
	}
}

func TestSymbolForCanonicalisesPathLikeFingerprint(t *testing.T) {
	d := Diff{Files: []FileDiff{{Path: "pkg/x.go", Hunks: []Hunk{{
		NewStart: 1, NewLines: 2, HeaderRaw: "func X()",
	}}}}}
	for _, in := range []string{"pkg/x.go", "pkg\\x.go", "././pkg/x.go", ".\\pkg\\x.go"} {
		if got := d.SymbolFor(in, 1); got != "func X()" {
			t.Errorf("SymbolFor(%q) = %q, want func X()", in, got)
		}
	}
}

func TestIsFuncLine(t *testing.T) {
	for s, want := range map[string]bool{
		"func f()": true, "_priv": true, "$var": true, "Class A": true,
		"": false, " indented": false, "\tindented": false, "}": false,
		"// comment": false, "#define X": false, "1abc": false,
	} {
		if got := isFuncLine(s); got != want {
			t.Errorf("isFuncLine(%q) = %v, want %v", s, got, want)
		}
	}
}
