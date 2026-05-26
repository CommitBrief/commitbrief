package diff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/ignore"
)

// Benchmarks for the local pipeline. PRD §7.1 targets:
//
//   - Parse + Filter + EstimateTokens on a 10k-line diff: < 200ms
//
// "10k lines" is the worth-being-explicit ceiling; we synthesize a diff
// that matches in shape (mix of additions, deletions, context lines,
// realistic file paths) so the numbers are comparable across runs.
//
// Run with:
//
//	go test -bench=. -benchmem -run=^$ ./internal/diff
//
// CONTRIBUTING.md "Performance" section explains how the numbers feed
// back into the PRD targets.

// generateDiff returns a synthetic unified diff with `files` files, each
// containing `hunksPerFile` hunks of `linesPerHunk` changed lines. Lines
// alternate +/- so both AddedLines() and DeletedLines() see real work.
// Total diff line count ≈ files × hunksPerFile × linesPerHunk × 2
// (each changed line emits one +/- plus its counterpart) plus header
// overhead (~5 lines per file, ~1 per hunk).
func generateDiff(files, hunksPerFile, linesPerHunk int) string {
	var sb strings.Builder
	// Pre-size for ~80 chars/line is generous; reduces reallocs during
	// the synthetic generation.
	sb.Grow(files * hunksPerFile * linesPerHunk * 80)

	for f := 0; f < files; f++ {
		path := fmt.Sprintf("internal/pkg%02d/file%03d.go", f%20, f)
		fmt.Fprintf(&sb, "diff --git a/%s b/%s\n", path, path)
		fmt.Fprintf(&sb, "index abc%04d..def%04d 100644\n", f, f)
		fmt.Fprintf(&sb, "--- a/%s\n", path)
		fmt.Fprintf(&sb, "+++ b/%s\n", path)
		for h := 0; h < hunksPerFile; h++ {
			start := h*20 + 1
			fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@ func Example%d()\n",
				start, linesPerHunk, start, linesPerHunk, f)
			for l := 0; l < linesPerHunk; l++ {
				switch l % 3 {
				case 0:
					fmt.Fprintf(&sb, "-\told := computeValue%d(%d)\n", f, l)
					fmt.Fprintf(&sb, "+\tnewer := computeValue%d(%d)\n", f, l)
				case 1:
					fmt.Fprintf(&sb, " \tif old != nil { return old }\n")
				case 2:
					fmt.Fprintf(&sb, "+\tlogger.Info(\"updated\", \"value\", newer)\n")
				}
			}
		}
	}
	return sb.String()
}

// sampleDiff is computed once per benchmark run via testMain init, so
// generation cost isn't counted in benchmark time. ~10k lines.
var benchDiff = git.Diff{Content: generateDiff(100, 5, 20)}

func BenchmarkParse10kLines(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(benchDiff); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFilter10kLines(b *testing.B) {
	parsed, err := Parse(benchDiff)
	if err != nil {
		b.Fatal(err)
	}
	matcher := ignore.Builtin()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Filter(parsed, matcher)
	}
}

func BenchmarkEstimateTokens10kLines(b *testing.B) {
	parsed, err := Parse(benchDiff)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parsed.EstimateTokens()
	}
}

// BenchmarkPipeline10kLines is the PRD §7.1 headline target: parse +
// filter + token estimate, the whole local pipeline. < 200ms target.
func BenchmarkPipeline10kLines(b *testing.B) {
	matcher := ignore.Builtin()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parsed, err := Parse(benchDiff)
		if err != nil {
			b.Fatal(err)
		}
		filtered := Filter(parsed, matcher)
		_ = filtered.EstimateTokens()
	}
}
