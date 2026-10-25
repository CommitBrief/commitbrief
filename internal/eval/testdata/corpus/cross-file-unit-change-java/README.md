# Fixture: cross-file-unit-change-java
**Category:** correctness · **Language:** Java

`PriceCalculator.total()` now returns integer cents instead of a dollar `double`. `OrderService` is updated, but `InvoiceService` (touched in the same diff for formatting) still computes tax as `total * 0.2` and prints it as dollars, so invoices are 100x too large. The defect sits in a different file than the contract change.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
