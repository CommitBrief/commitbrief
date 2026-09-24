# Fixture: clean-dep-bump-go
**Category:** (none — clean diff) · **Language:** Go

A routine dependency bump in `go.mod`/`go.sum` (`golang.org/x/time`
v0.5.0 → v0.6.0), no changelog red flags, no API usage changes elsewhere
in the diff. A good review must stay **silent**.

Clean dep-bump criterion (all three hold for every version in the diff,
target and context alike):

- **Exists in the registry:** proxy.golang.org `.info`; the `go.sum` hashes
  are the real ones from sum.golang.org.
- **Predates model training cutoffs:** x/time v0.5.0 2023-11-21, v0.6.0
  2024-07-16; context: cobra v1.8.1 2024-06-01, viper v1.19.0 2024-06-01,
  x/sync v0.8.0 2024-07-16.
- **No known advisories:** OSV `api.osv.dev/v1/query` returns `{}` for
  every one of those versions (checked 2026-09-24). The earlier target,
  `golang.org/x/text` v0.18.0, is covered by GO-2026-5970 and was dropped.

**Provenance:** hand-authored clean control (ADR-0018 §1).
