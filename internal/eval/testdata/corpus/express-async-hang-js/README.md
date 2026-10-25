# Fixture: express-async-hang-js

**Category:** error-handling · **Language:** JavaScript · **Class:** framework-idiomatic

The new `/reports/:id/export` route is an `async` Express 4 handler with no error handling.
If `db.getReport` or `renderPdf` rejects, Express 4 never sees the error, the rejection is unhandled and
the client connection hangs. The sibling route that uses the project's `wrap()` helper is fine.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 14 lines.
