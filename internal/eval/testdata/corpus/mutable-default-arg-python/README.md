# Fixture: mutable-default-arg-python
**Category:** correctness · **Language:** Python
add_item declares a mutable list default argument, causing state to leak and accumulate across separate calls. Hand-authored planted defect (ADR-0018 §1).
