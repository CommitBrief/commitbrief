# Fixture: cross-file-money-object-php
**Category:** correctness · **Language:** PHP

`Cart::total()` now returns a `Money` value object instead of an int of cents. `CheckoutController` is updated, but `PaymentGateway` (also in the diff, for an unrelated logging change) still multiplies the result as a number and will throw a TypeError. The break is in a different file from the contract change.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
