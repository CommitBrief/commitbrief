# Fixture: prototype-pollution-merge-js

**Category:** security · **Language:** JavaScript

A "deep merge" helper is added to combine saved user preferences with request-supplied
overrides. It recurses into any key, including `__proto__` and `constructor`, so a JSON body such as
`{"__proto__": {"isAdmin": true}}` pollutes `Object.prototype` for the whole process.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 20 lines.
