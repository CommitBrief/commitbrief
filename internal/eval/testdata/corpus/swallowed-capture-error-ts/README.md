# Fixture: swallowed-capture-error-ts

**Category:** error-handling · **Language:** TypeScript

The checkout flow wraps `gateway.capture` in a `try/catch` that only logs a warning, then
unconditionally marks the order `paid`. A declined or timed-out capture ships goods that were never
paid for. The `try/catch` around the receipt email, below, is a legitimate best-effort swallow and must
stay silent.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 14 lines.
