# Fixture: redos-email-regex-ts

**Category:** security · **Language:** TypeScript

A hand-rolled email validator uses nested quantifiers (`([a-zA-Z0-9]+[._-]?)+`). On a long
alphanumeric string that fails to match (no `@`), V8's backtracking is exponential, so a single
signup request with a ~40 character payload pins the event loop. The unrelated, linear phone regex
below it must stay silent.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 10 lines.
