# Fixture: unchecked-json-decode-php
**Category:** error-handling · **Language:** PHP

Config loading ignores the `false` return of `file_get_contents` and the `null` return of `json_decode`, then indexes into the result; a missing or malformed file raises a confusing error far from the cause.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
