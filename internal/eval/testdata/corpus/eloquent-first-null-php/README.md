# Fixture: eloquent-first-null-php
**Category:** correctness · **Language:** PHP

A lookup uses `->first()` and immediately reads a property on the result. For an unknown email `first()` returns `null`, producing an error instead of a handled 404 (`firstOrFail()` was intended).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
