// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"fmt"
)

// Verdict is the review-level action submitted to GitHub.
type Verdict int

const (
	// VerdictApprove — no findings reached the request-changes threshold.
	VerdictApprove Verdict = iota
	// VerdictComment — findings exist but none reached the threshold.
	VerdictComment
	// VerdictRequestChanges — at least one finding reached the threshold.
	VerdictRequestChanges
)

// ghFlag maps a Verdict to the `gh pr review` flag.
func (v Verdict) ghFlag() string {
	switch v {
	case VerdictApprove:
		return "--approve"
	case VerdictRequestChanges:
		return "--request-changes"
	default:
		return "--comment"
	}
}

// BuildReviewBody returns the fixed-English review body for a verdict
// (ADR-0016 §6.4).
func BuildReviewBody(v Verdict, whoami string) string {
	switch v {
	case VerdictApprove:
		return fmt.Sprintf("@%s %s", whoami, signature)
	case VerdictRequestChanges:
		return fmt.Sprintf("We can revisit it after we've solved the problems. @%s %s", whoami, signature)
	default:
		return fmt.Sprintf("It must be checked by the human eye. @%s %s", whoami, signature)
	}
}

// SubmitReview submits the review-level verdict via `gh pr review`.
func SubmitReview(ctx context.Context, r Runner, id, repo string, v Verdict, body string) error {
	_, err := r.Run(ctx, repoArgs(repo, "pr", "review", id, v.ghFlag(), "-b", body)...)
	return err
}
