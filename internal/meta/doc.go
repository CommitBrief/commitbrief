// SPDX-License-Identifier: GPL-3.0-or-later

// Package meta turns the live binary into a machine-readable inventory of the
// CLI surface: providers, commands, flags, configuration keys and the MCP
// review tool's arguments.
//
// It exists because documentation drifted from code (ADR-0039). Prose in
// README.md, the wiki and the site claimed provider counts, flag names and
// model ids that nobody re-derived when the code moved. Everything here is
// read out of the same structures the binary actually runs on, so a rendered
// docs block cannot describe a CLI that does not exist.
//
// # No import cycle
//
// Build takes the cobra root and the MCP tool schema as PARAMETERS. meta must
// never import internal/cli, because internal/cli is what calls Build (behind
// the hidden --gen-surface flag). The dependency runs cli -> meta, one way.
//
// # The registry trap
//
// provider.AllMetadata reports only what has been linked in, and the metadata
// registry is populated by the blank imports in cmd/commitbrief/main.go. A
// test in this package that reads it without those blank imports observes an
// empty registry and passes vacuously — which is why meta's own tests carry
// them and assert all ten shipped providers are present.
//
// # No build stamp
//
// Nothing here records a version, commit or build date. The generated
// artifact (internal/meta/surface.json) is committed to git; a build-stamped
// field would change on every build, churn in every diff and make the drift
// check fail on a clean tree.
package meta

//go:generate bash ../../scripts/docs-gen.sh
