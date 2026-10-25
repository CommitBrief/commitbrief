# Fixture: refactor-batch-clamp-go

**Category:** correctness · **Language:** Go · **Class:** large-refactor

Large refactor of `Import`: the monolithic function is split into `parseOrders`, `chunkOrders`
and `insertBatch`. The old loop clamped the final slice bound (`if end > len(orders)`); the extracted
`chunkOrders` lost that clamp, so any import whose size is not a multiple of the batch size slices past
the end and panics. One real defect inside an otherwise behavior-preserving ~170-line diff.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 158 lines.
