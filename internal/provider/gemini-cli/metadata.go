// SPDX-License-Identifier: GPL-3.0-or-later

package geminicli

import "github.com/CommitBrief/commitbrief/internal/provider"

// Metadata describes the gemini-cli backend for documentation. It needs no
// credential and never shells out to the host CLI, so it answers even on a
// machine where `gemini` is not installed. See ADR-0039.
//
// Binary is binaryName, the same constant this package's clireview.Spec is
// built from, so the published name cannot drift from the one exec.LookPath
// actually resolves.
//
// Models is empty and DefaultModel is left blank on purpose: the host CLI
// owns model selection, and clireview.DefaultModel() synthesises a
// "binary@version" string for the cache key at runtime. That identifier is
// not a model, and publishing it as one would send readers looking for a
// model name they can pass to --model.
func Metadata() provider.Metadata {
	return provider.Metadata{
		Name:   Name,
		Kind:   provider.KindCLI,
		Binary: binaryName,
	}
}
