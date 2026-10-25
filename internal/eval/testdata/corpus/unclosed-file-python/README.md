# Fixture: unclosed-file-python
**Category:** resource-leak · **Language:** Python

A file is opened with a bare `open()` and closed at the end of the function, but a new early `return` before the `close()` leaks the handle on that path (and on any exception).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
