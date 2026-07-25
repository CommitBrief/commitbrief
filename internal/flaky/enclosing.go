// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// EnclosingTest resolves the name of the Go test function containing line
// (1-indexed) in the file at path, by parsing the file with go/parser and
// walking its declarations for the *ast.FuncDecl whose position range
// contains line.
//
// Go only (round 3 of ADR-0033 §8/§10's helper). Resolving every language
// required a hand-rolled, multi-language regex-and-scan implementation that
// went through three rounds of distinct lexical bugs -- ASCII-only
// identifier classes, braces inside string/comment spans, an unescaped
// backtick in a JS template literal -- each fix correct and each followed by
// another, with no evidence of convergence: a slow-motion reimplementation
// of a lexer, one edge case at a time. go/parser is the real Go compiler
// frontend, so strings, comments, raw literals, escapes, and Unicode
// identifiers are all handled exactly, by construction, not approximated.
//
// This is a deliberate, approved reduction in scope for every other
// language, not a regression: EnclosingTest now always returns ("", false)
// for non-Go files. Non-Go test files keep full static flaky detection
// (internal/flaky's rule-based Detector, unaffected by this file); they
// simply never get sandbox-rerun confirmation, which is exactly the
// fallback ADR-0033 §10 already specifies for an unresolved name -- skip
// the rerun for that finding, keep the bare static finding, unannotated.
//
// It reads the WORKING TREE, not the staged snapshot (ADR-0033 §8): the
// command a caller binds will run against the worktree too, so resolving
// against the same bytes keeps the name and the execution consistent.
//
// Returns ok=false — never a guess — when the file cannot be read, is not
// Go, fails to parse, line falls outside every function's body, or falls
// inside a function that is not one `go test -run '^NAME$'` can actually
// select and re-execute on its own (see isRerunnableTestFunc). A wrong name
// would run the wrong test and yield a confident, wrong verdict, which is
// worse than no verdict (ADR-0033 §10).
func EnclosingTest(path string, line int) (string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the diff being reviewed
	if err != nil {
		return "", false
	}
	if detectLang(path) != "go" {
		return "", false
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if err != nil {
		// A half-edited or otherwise invalid file must degrade safely.
		// parser.ParseFile can return a non-nil best-effort *ast.File
		// alongside the error; it is deliberately not used here.
		return "", false
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		if line < start || line > end {
			continue
		}
		// Go disallows nested func declarations, so at most one top-level
		// FuncDecl's line range can ever contain a given line -- no need to
		// keep scanning for a "better" match once one is found.
		return isRerunnableTestFunc(fn)
	}
	return "", false
}

// isRerunnableTestFunc decides whether fn is a function `go test -run
// '^NAME$'` can select and re-execute on its own -- the one property that
// matters here, since that is exactly what a bound sandbox_command
// re-invokes (ADR-0033 §1). Three judgement calls, made explicit:
//
//   - Test* and Fuzz* only. Both are matched by `-run` per `go help
//     testflag` ("run only those tests, examples, and fuzz tests..."), and
//     both have the "run it, observe pass/fail, repeat" shape sandbox-rerun
//     exists for. Benchmark* is excluded: `-run` does not select benchmarks
//     at all (a *different* flag, -bench, does), so reporting one would
//     render a `-run` pattern that matches zero tests -- `go test` exits 0,
//     which classifies as VerdictTransient ("passed every rerun") when in
//     truth nothing configured ever ran (exactly the failure chain ADR-0033
//     §10 names). Example* is excluded too: `-run` does technically select
//     it, but its pass/fail model is output comparison, not the
//     run/observe/repeat model sandbox-rerun exists to confirm, and the
//     static detector's own rules (hard-sleep, unseeded-random,
//     brittle-selector, time-assertion, over-mock) are not realistically
//     going to fire inside a doctest-style Example body -- excluding it
//     costs nothing real and keeps the recognized shape minimal.
//   - The exact `go test` naming rule, not just a prefix check: "Test" or
//     "TestFoo" count, "Testfoo" does not (the rune immediately after the
//     prefix must not be lowercase -- replicated from
//     cmd/go/internal/load's own isTest). A merely Test-shaped name that
//     `go test` itself would not register as a test is exactly the
//     phantom-target failure this function exists to avoid.
//   - No receiver, and exactly one parameter of the matching *testing.T /
//     *testing.F type. A receiver method -- the shape a testify-suite test
//     takes, e.g. `func (s *MySuite) TestThing()` -- is never itself
//     directly selectable via `go test -run`; only the outer
//     `TestMySuite(t *testing.T)` that calls suite.Run is a real, runnable
//     go test function. Resolving to the method's own name would produce a
//     plausible-looking name `-run` can never match. The parameter check
//     catches the same failure for a plain function that merely has a
//     Test-shaped name but the wrong signature, which `go test` also never
//     registers as a test.
func isRerunnableTestFunc(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv != nil {
		return "", false
	}
	if fn.Type.TypeParams != nil {
		return "", false
	}

	name := fn.Name.Name
	var want string
	switch {
	case isGoTestName(name, "Test"):
		want = "T"
	case isGoTestName(name, "Fuzz"):
		want = "F"
	default:
		return "", false
	}

	paramType, ok := singleParam(fn.Type.Params)
	if !ok || !isTestingParam(paramType, want) {
		return "", false
	}
	return name, true
}

// isGoTestName replicates `go test`'s own naming rule (cmd/go/internal/load's
// isTest): name must start with prefix, and if anything follows, the first
// rune of what follows must not be a lowercase letter -- so "TestFoo" and
// bare "Test" count, but "Testfoo" does not.
func isGoTestName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

// singleParam returns the type expression of a parameter list's sole
// parameter, or ok=false when the list has zero parameters or more than one.
func singleParam(fl *ast.FieldList) (ast.Expr, bool) {
	if fl == nil || len(fl.List) != 1 {
		return nil, false
	}
	f := fl.List[0]
	if len(f.Names) > 1 {
		return nil, false
	}
	return f.Type, true
}

// isTestingParam reports whether expr is *testing.<letter> (e.g. *testing.T
// for a Test function, *testing.F for a Fuzz function).
func isTestingParam(expr ast.Expr, letter string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "testing" && sel.Sel.Name == letter
}
