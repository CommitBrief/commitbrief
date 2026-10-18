// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"fmt"
	"regexp"
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

// reviewedMarkerRe matches the hidden K8 marker `<!-- commitbrief:reviewed=<oid> -->`
// that every review body ends with, so a later --since-last-review run can tell
// which head commit the review covered.
var reviewedMarkerRe = regexp.MustCompile(`<!--\s*commitbrief:reviewed=([0-9a-fA-F]{7,64})\s*-->`)

// ReviewedMarker returns the hidden marker recording that a review covered
// the PR up to head commit oid.
func ReviewedMarker(oid string) string {
	return "<!-- commitbrief:reviewed=" + oid + " -->"
}

// WithReviewedMarker appends the K8 marker for oid to a review body. An empty
// oid leaves the body unchanged.
func WithReviewedMarker(body, oid string) string {
	if oid == "" {
		return body
	}
	return body + "\n\n" + ReviewedMarker(oid)
}

// ParseReviewedMarker extracts the head OID from a review body. When a body
// holds several markers the last one wins.
func ParseReviewedMarker(body string) (string, bool) {
	all := reviewedMarkerRe.FindAllStringSubmatch(body, -1)
	if len(all) == 0 {
		return "", false
	}
	return all[len(all)-1][1], true
}
