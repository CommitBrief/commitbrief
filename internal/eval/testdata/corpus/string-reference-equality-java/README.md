# Fixture: string-reference-equality-java
**Category:** correctness · **Language:** Java

A role check compares strings with `==`. It passes for interned literals in tests but fails for values read from a token or database.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
