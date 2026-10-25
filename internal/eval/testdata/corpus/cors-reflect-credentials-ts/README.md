# Fixture: cors-reflect-credentials-ts

**Category:** security · **Language:** TypeScript

CORS is "fixed" for a staging frontend by switching to `origin: true` together with
`credentials: true`. The `cors` package then reflects whatever `Origin` the browser sends and allows
cookies, so any website a logged-in user visits can read authenticated API responses cross-origin.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 5 lines.
