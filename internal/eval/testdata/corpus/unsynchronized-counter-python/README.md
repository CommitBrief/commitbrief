# Fixture: unsynchronized-counter-python
**Category:** concurrency · **Language:** Python

A module-level request counter is incremented from a thread pool with `+=` and no lock. The read-modify-write is not atomic, so increments are lost under load.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
