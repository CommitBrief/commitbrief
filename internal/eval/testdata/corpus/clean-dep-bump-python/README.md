# Fixture: clean-dep-bump-python
**Category:** (none — clean diff) · **Language:** Python

A routine patch-level dependency bump in `requirements.txt` (`pydantic`
2.8.1 → 2.8.2). A good review must stay **silent**.

Clean dep-bump criterion (all three hold for every version in the diff,
target and context alike):

- **Exists in the registry:** pypi.org.
- **Predates model training cutoffs:** pydantic 2.8.1 2024-07-03, 2.8.2
  2024-07-04; context: alembic 1.13.2 2024-06-26, gunicorn 22.0.0
  2024-04-16, python-dateutil 2.9.0.post0 2024-03-01, sqlalchemy 2.0.32
  2024-08-05 (PyPI upload time).
- **No known advisories:** OSV `api.osv.dev/v1/query` returns `{}` for
  every one of those versions (checked 2026-09-24). The earlier choices
  were dropped: `requests` 2.32.3 (GHSA-9hjg-9r4m-mvj7 and three more) and
  the `flask` 3.0.3 context line (GHSA-68rp-wp8r-4726).

**Provenance:** hand-authored clean control (ADR-0018 §1).
