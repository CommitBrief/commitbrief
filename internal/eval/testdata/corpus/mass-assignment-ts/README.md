# Fixture: mass-assignment-ts

**Category:** security · **Language:** TypeScript

`PATCH /users/:id` passes the raw request body straight to `users.update`. A client can include
`role: "admin"` or `emailVerified: true` and promote itself — classic mass assignment. The handler should
whitelist the editable fields.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 7 lines.
