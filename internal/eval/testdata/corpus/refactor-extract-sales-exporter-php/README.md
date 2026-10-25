# Fixture: refactor-extract-sales-exporter-php
**Category:** correctness · **Language:** PHP

A large refactor moves the sales CSV export (filtering, sorting, pagination, totals) from `ReportService` into a new `SalesExporter` class (about 170 changed lines). Everything is copied verbatim except the pagination offset, which changed from `($page - 1) * $perPage` to `$page * $perPage`, so page 1 skips the first page of rows.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
