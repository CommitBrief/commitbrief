# Fixture: foreach-async-js

**Category:** correctness · **Language:** JavaScript

The notification fan-out iterates with `recipients.forEach(async ...)`. `forEach` ignores the
returned promises, so the handler responds `200` before any mail is sent, failures are unhandled
rejections that crash newer Node versions, and `sent` is read before it is incremented.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 9 lines.
