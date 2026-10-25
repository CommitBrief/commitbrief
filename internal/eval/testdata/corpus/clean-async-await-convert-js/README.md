# Fixture: clean-async-await-convert-js

**Category:** (none — clean diff) · **Language:** JavaScript

Converts a promise chain to `async/await` with identical semantics: same error propagation
(the `catch` still rethrows a wrapped error), same ordering (sequential, not parallel), and the early return
for the empty cart is preserved. A good review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
