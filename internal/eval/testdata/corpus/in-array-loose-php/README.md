# Fixture: in-array-loose-php
**Category:** correctness · **Language:** PHP

Role membership is checked with `in_array` without the strict flag. Loose comparison treats mixed types as equal (for example `'1e1' == '10'` or `0 == 'admin'` on older PHP), so unexpected ids pass the check.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
