# Fixture: refactor-pagination-offset-ts

**Category:** correctness · **Language:** TypeScript · **Class:** large-refactor

Refactor of the listing service: inline filtering/sorting/pagination is extracted into
`applyFilters`, `sortListings` and `paginate`. The old code computed `(page - 1) * pageSize` because pages
are 1-based; the new `paginate` helper uses `page * pageSize`, silently skipping the first page of results
on every request. One real defect inside an otherwise behavior-preserving ~170-line diff.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 155 lines.
