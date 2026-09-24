# Fixture: clean-extract-helper-go
**Category:** (none — clean diff) · **Language:** Go

A pure refactor that extracts the duplicate discount-clamping logic into a
small `clampDiscount` helper. Behavior is identical for every input; a good
review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
