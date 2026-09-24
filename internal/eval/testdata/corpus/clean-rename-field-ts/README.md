# Fixture: clean-rename-field-ts
**Category:** (none — clean diff) · **Language:** TypeScript
**Held-out:** yes

A pure interface field rename (`qty` -> `quantity`) propagated consistently
to its one use site. `cartTotal` is module-internal (not exported), so the
rename carries no external-caller breakage risk. Semantically identical. A
good review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
