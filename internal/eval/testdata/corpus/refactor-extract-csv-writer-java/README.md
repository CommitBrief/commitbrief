# Fixture: refactor-extract-csv-writer-java
**Category:** correctness · **Language:** Java

A large refactor extracts CSV rendering from `ReportService` into `CsvReportWriter` (about 170 changed lines). The code is moved verbatim except `averageUnits`, where `(double) units / rows.size()` became `units / rows.size()`: integer division truncates the average before it is widened to double.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
