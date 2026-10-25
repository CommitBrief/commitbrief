# Fixture: unvalidated-page-js

**Category:** correctness · **Language:** JavaScript

The listing route converts `req.query.page` with `Number(req.query.page) || 1`. That only
guards against `0`, `NaN` and the empty string: `-3`, `1.5` and `Infinity` pass straight through, and
`listings.page` computes `OFFSET = (page - 1) * size`. A negative page produces a negative OFFSET, which
Postgres rejects with a 500; a fractional page yields a wrong page or a non-integer OFFSET (e.g. `1.03`);
`Infinity` yields a bogus OFFSET and a database error. Low
severity: input validation gap, no data exposure.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 6 lines.
