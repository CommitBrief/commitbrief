# Fixture: string-concat-in-loop-go
**Category:** performance · **Language:** Go
Build accumulates the result with += inside a for loop instead of strings.Builder, causing O(n^2) allocations. Hand-authored planted defect (ADR-0018 §1).
