# Fixture: cross-file-percent-bps-go

**Category:** correctness · **Language:** Go · **Class:** cross-file

Two files change together. `pricing.Apply` is re-scaled from whole percent to basis points
(`bps/10000`), but the new call site in `internal/checkout/cart.go` still passes `PercentOff`, a whole
percent (the struct comment says 0-100). A 10% coupon is now applied as 0.1%. The breakage is in the
caller, not in the function that changed.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 25 lines.
