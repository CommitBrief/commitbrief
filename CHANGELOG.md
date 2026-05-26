# Changelog

All notable changes to **CommitBrief** are documented in this file.

The format follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

> Tags prior to **v0.4.0** were cut in the private repository and produced no
> public artifacts; the first publicly released version is v0.4.0.

## [Unreleased]

### Changed
- Go module `go` directive raised from `1.23` to `1.25` to track the
  modern Go toolchain expected by upstream dependencies (`go-git/v5`,
  `golang.org/x/net`). Supersedes ADR-0013 §2's original Go 1.23 target.

### Added
- Go module `github.com/CommitBrief/commitbrief` targeting `go 1.25`.
- Directory skeleton: `cmd/commitbrief/`, `internal/{cli,config,rules,...}`,
  `testdata/`, `scripts/`.
- Standard project files: `LICENSE` (GPL-3.0-or-later), `.gitignore`,
  `.editorconfig`, `README.md`, `Makefile`, `.golangci.yml`, `.goreleaser.yaml`.
- Repo-level `CONTRIBUTING.md` linking to the org-wide contribution guide.
- GitHub Actions workflows: `ci.yml` (build + test + lint matrix on
  ubuntu/macos/windows), `release.yml` (goreleaser, `--skip=publish` until
  v0.4.0), `codeql.yml`.
- Helper scripts: `scripts/release-check.sh`, `scripts/license-check.sh`,
  `scripts/manpage.sh`.

[Unreleased]: https://github.com/CommitBrief/commitbrief/commits/main
