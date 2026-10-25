# Fixture: check-then-act-concurrent-map-java
**Category:** concurrency · **Language:** Java

A cache uses a `ConcurrentHashMap` but does `containsKey` then `put` as two steps. Two threads can both miss and both compute, and the second silently overwrites the first.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
