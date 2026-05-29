# Fixture: unchecked-type-assert-go
**Category:** correctness · **Language:** Go
A single-value type assertion i.(string) panics at runtime when the interface holds a non-string value. Hand-authored planted defect (ADR-0018 §1).
