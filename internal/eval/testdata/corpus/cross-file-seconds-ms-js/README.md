# Fixture: cross-file-seconds-ms-js

**Category:** correctness · **Language:** JavaScript · **Class:** cross-file

Two files change together. `getTimeout()` in `lib/config.js` now returns milliseconds (the env
var `REQUEST_TIMEOUT` is still configured in seconds and converted inside), but the new call in `lib/client.js` still multiplies by 1000 as
when the value was in seconds — the abort timer is 1000x too long (about 8 hours for the 30s default), so
the timeout never fires in practice.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 19 lines.
