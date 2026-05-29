# Fixture: off-by-one-loop-go
**Category:** correctness · **Language:** Go
The loop bound i <= len(s) indexes one past the end of the string, causing an index-out-of-range panic. Hand-authored planted defect (ADR-0018 §1).
