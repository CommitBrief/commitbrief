# Fixture: interrupted-exception-swallowed-java
**Category:** concurrency · **Language:** Java

A retry loop catches `InterruptedException` and just continues, discarding the interrupt flag, so executor shutdown can never stop the loop.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
