# Fixture: loose-comparison-token-php
**Category:** security · **Language:** PHP

A signature check compares two hex digests with `==`. PHP's loose comparison treats strings like `0e123...` as the number zero, so two different 'magic hash' values compare equal (and the comparison is not constant time either).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
