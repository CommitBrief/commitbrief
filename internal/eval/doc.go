// SPDX-License-Identifier: GPL-3.0-or-later

// Package eval is CommitBrief's review-quality eval harness (ADR-0018).
//
// It scores a provider's actual review output against a curated
// known-answer corpus and reports precision / recall / false-positive
// rate. The corpus lives under testdata/corpus/<name>/, one directory per
// fixture: input.diff (the change under review), expected.json (the answer
// key), and, for the deterministic tier, mock_response.json (scripted
// findings fed to the mock provider).
//
// Two execution tiers (ADR-0018 §3):
//
//   - Deterministic tier — TestEvalMockCorpus runs the corpus through the
//     mock provider with each fixture's scripted response. It validates the
//     harness, matcher, and scoring math, runs in plain `go test ./...`, and
//     is therefore part of the CI gate. It does NOT measure model quality.
//
//   - Live tier — TestEvalLive (behind the `live` build tag, like the rest
//     of the live provider tests) runs the corpus through a real provider
//     and prints the quality scorecard. Non-deterministic and gated; it is
//     the source of the README quality numbers, never a CI gate.
//
// The harness consumes provider output through the locked --json schema v1
// findings[] (ADR-0014); it introduces no new output contract.
package eval
