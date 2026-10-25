# Fixture: shared-simpledateformat-java
**Category:** concurrency · **Language:** Java

A `static final SimpleDateFormat` is shared by request threads. `SimpleDateFormat` is not thread-safe, so concurrent parse/format calls corrupt each other's results.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
