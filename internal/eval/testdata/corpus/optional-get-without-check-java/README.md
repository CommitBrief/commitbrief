# Fixture: optional-get-without-check-java
**Category:** correctness · **Language:** Java

A lookup returns an `Optional`, and a new caller calls `.get()` directly. For unknown ids this throws `NoSuchElementException` and the API answers 500.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
