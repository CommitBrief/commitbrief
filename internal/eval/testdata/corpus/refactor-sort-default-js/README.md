# Fixture: refactor-sort-default-js

**Category:** correctness · **Language:** JavaScript · **Class:** large-refactor

Refactor of the latency summary module: the monolithic `summarize` is split into `groupByRoute`,
`percentile`, `mean` and `describe`. The old code sorted with a numeric comparator `(a, b) => a - b`; the
extracted `percentile` calls `[...values].sort()` with no comparator, which sorts numbers as strings
(`[9, 10, 100]` sorts as `[10, 100, 9]`), so
p95/p99 are wrong for any realistic data (`median` keeps its numeric comparator). One real defect in a
~165-line behavior-preserving diff.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 168 lines.
