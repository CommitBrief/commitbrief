# Benchmarks

Baseline performance numbers captured at the **v1.0.0-rc.1** API
freeze checkpoint. These are not contractual SLAs — they are a
regression detector. If a future change makes any of these numbers
2× slower, that probably means the hot path grew an extra walk and
should be investigated. The PRD §7.1 targets (cache hit < 100 ms,
local pipeline < 50 ms for a 10k-line diff) are the user-facing
bar; the per-operation numbers below are the components.

Regenerate locally with:

```sh
make bench
```

## Reference hardware

| Field         | Value                  |
|---------------|------------------------|
| CPU           | Apple M4 Pro           |
| GOOS / GOARCH | darwin / arm64         |
| Go            | 1.25                   |
| Captured      | v1.0.0-rc.1 (2026-05-27) |

Apple Silicon is the maintainer's primary machine. Linux/amd64 CI
runners typically land within ~1.5× of these numbers; Windows is
closest to Linux. Re-baseline if the reference hardware changes.

## Diff pipeline (`internal/diff`)

10 000 added / 10 000 deleted lines across ~100 files.

| Benchmark                   |       ns/op |    B/op |  allocs/op | Notes |
|-----------------------------|------------:|--------:|-----------:|-------|
| `BenchmarkParse10kLines`    |     699 121 | 1 633 383 |     19 110 | One-pass parse; allocations dominated by per-line file/hunk structs. |
| `BenchmarkFilter10kLines`   |   1 601 347 |  32 592 |          8 | Built-in ignore matcher; pre-compiled regex pool. |
| `BenchmarkEstimateTokens10kLines` | 10 269 |       0 |          0 | Tokens estimator — chars/4 heuristic, zero-alloc. |
| `BenchmarkPipeline10kLines` |   2 231 184 | 1 665 979 |     19 118 | Parse → Filter → KeepPaths end-to-end. |

The 2.2 ms pipeline number is what feeds the dry-run report and the
secret-scan + cost-preflight pre-checks. Comfortably under PRD §7.1's
50 ms local-pipeline target with 20× headroom for diff-size growth.

## Cache (`internal/cache`)

| Benchmark              |      ns/op |  B/op | allocs/op | Notes |
|------------------------|-----------:|------:|----------:|-------|
| `BenchmarkCacheHit`    |     12 966 | 1 864 |        22 | JSON decode + TTL check; happy path on a hit. |

A 13 µs cache hit means the "Saved: $X" verbose footer fires within
the same frame as the spinner clears. Well under PRD §7.1's 100 ms
cache-hit budget — that budget targets the worst-case `--no-cache`
miss path that fires a provider call.

## How regressions surface

`make check` does NOT run benchmarks (they're slow and noisy in
parallel) — they live behind `make bench`. The intent is that this
file gets refreshed manually at every minor release, and the
appearance of a 2×+ regression in a PR is the trigger for an
investigation. We deliberately avoid auto-failing CI on benchmark
drift; CPU variance between runners would generate too many false
alarms.
