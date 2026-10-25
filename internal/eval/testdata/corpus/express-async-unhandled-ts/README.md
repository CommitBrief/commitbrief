# Fixture: express-async-unhandled-ts

**Category:** error-handling · **Language:** TypeScript · **Class:** framework-idiomatic

The refund route is an `async` Express 4 handler with no `try/catch` and not wrapped in the
file's `asyncHandler`. Any rejection from `findById`, `refund` or `markRefunded` becomes an unhandled
promise rejection and the request hangs until the client times out. The route above it is correctly wrapped
and must not be flagged.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 17 lines.
