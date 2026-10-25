# Fixture: unawaited-promises-ts

**Category:** correctness · **Language:** TypeScript

While converting `reserveAll` from `await` to `.then` the author stopped awaiting the
reservation. Each `reserveStock(...).then(...)` promise is neither awaited nor returned, so the loop ends
immediately, `orders.confirm` runs before any reservation completes, and the `OutOfStockError` thrown
inside the callback becomes an unhandled rejection instead of aborting the order. Type-checks cleanly,
which is why it reaches review.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 12 lines.
