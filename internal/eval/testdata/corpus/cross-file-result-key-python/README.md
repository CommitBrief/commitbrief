# Fixture: cross-file-result-key-python
**Category:** correctness · **Language:** Python

`charge()` now returns `charge_id` instead of `id`. The checkout caller is updated, but `receipts.py` (touched in the same diff for an unrelated log change) still reads `result["id"]` and will raise KeyError. The break is in a different file from the change.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
