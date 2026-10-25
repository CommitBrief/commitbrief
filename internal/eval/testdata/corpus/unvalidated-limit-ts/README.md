# Fixture: unvalidated-limit-ts

**Category:** correctness · **Language:** TypeScript

The activity feed reads `limit` with `parseInt(req.query.limit as string)` and hands it to the
query builder. A missing or non-numeric value yields `NaN` (the query builder drops the limit and returns
the whole table) and there is no upper bound, so `?limit=1000000` is a cheap way to dump or stall the
endpoint.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 8 lines.
