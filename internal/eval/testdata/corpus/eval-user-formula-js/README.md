# Fixture: eval-user-formula-js

**Category:** security · **Language:** JavaScript

Custom pricing formulas stored per tenant are evaluated with `new Function`. The formula
string comes from an admin-editable settings page (and from CSV imports), so any tenant admin can run
arbitrary JavaScript in the server process: read `process.env`, require `child_process`, and so on.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 6 lines.
