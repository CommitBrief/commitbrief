# Fixture: check-then-act-coupon-ts

**Category:** concurrency · **Language:** TypeScript

`redeemCoupon` reads whether the code was already used, then writes the redemption and credits
the wallet in separate awaits. Two concurrent requests with the same code both pass the check before
either writes, so the credit is granted twice. The redemption needs an atomic insert with a unique
constraint (or a transaction).

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 11 lines.
