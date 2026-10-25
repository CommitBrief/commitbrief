# Fixture: falsy-zero-qty-ts

**Category:** correctness · **Language:** TypeScript

`updateCartLine` defaults a missing quantity with `input.qty || 1`. A quantity of `0` — the
documented way for the client to remove a line — is falsy and silently becomes `1`, so lines can never be
removed. `??` is the correct operator. Low severity: wrong-but-not-dangerous behavior that unit tests
using qty >= 1 never exercise.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 8 lines.
