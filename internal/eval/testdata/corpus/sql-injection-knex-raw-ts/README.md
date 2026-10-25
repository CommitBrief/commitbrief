# Fixture: sql-injection-knex-raw-ts

**Category:** security · **Language:** TypeScript

Product search interpolates the `q` and `sort` query parameters into a `knex.raw` template
string. `q` is quoted by hand (breakable with a single quote) and `sort` is spliced into `ORDER BY`
verbatim. Parameter bindings and an allow-list for the sort column are required.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 9 lines.
