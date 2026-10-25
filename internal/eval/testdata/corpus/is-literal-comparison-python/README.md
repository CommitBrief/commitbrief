# Fixture: is-literal-comparison-python
**Category:** correctness · **Language:** Python

A status check compares a string with `is` against a literal. It works by accident for interned strings but fails for strings built at runtime (for example read from JSON).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
