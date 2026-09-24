# Fixture: clean-dep-bump-js
**Category:** (none — clean diff) · **Language:** JavaScript

A routine patch-level dependency bump in `package.json` (`zod` 3.23.7 →
3.23.8). A good review must stay **silent**.

Clean dep-bump criterion (all three hold for every version in the diff,
target and context alike):

- **Exists in the registry:** registry.npmjs.org.
- **Predates model training cutoffs:** zod 3.23.7 2024-05-07, 3.23.8
  2024-05-08; context: express 4.21.2 2024-12-05 (npm `time` metadata).
- **No known advisories:** OSV `api.osv.dev/v1/query` returns `{}` for
  every one of those versions (checked 2026-09-24). The earlier choices
  were dropped: `lodash` 4.17.21 (GHSA-f23m-r3pf-42rh, GHSA-r5fr-rjxr-66jc,
  GHSA-xxjr-mmjv-4gpg) and the `express` 4.19.2 context line
  (GHSA-qw6h-vgh9-j6wx).

**Provenance:** hand-authored clean control (ADR-0018 §1).
