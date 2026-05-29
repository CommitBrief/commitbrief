// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// signature is appended to every GitHub-posted message. `#CommitBrief`
// is literal text — the leading `#` is non-numeric so GitHub does not
// auto-link it to an issue (ADR-0016 §10).
const signature = "by #CommitBrief"

// BuildCommentBody renders one finding as an inline review comment body.
// Fixed English (ADR-0016 §10):
//
//	[SEVERITY] - Title
//	Description
//	Suggestion @whoami by #CommitBrief
func BuildCommentBody(f render.Finding, whoami string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] - %s\n", strings.ToUpper(string(f.Severity)), f.Title)
	fmt.Fprintf(&b, "%s\n", f.Description)
	fmt.Fprintf(&b, "%s @%s %s", f.Suggestion, whoami, signature)
	return b.String()
}

// CommentRequest is one inline comment to POST. RepoSlug is the PR's
// baseRepository ("owner/name", cross-fork correctness); CommitID is the
// head OID the diff was fetched at. Side is "RIGHT" (new file) or "LEFT"
// (old file); empty defaults to RIGHT.
type CommentRequest struct {
	RepoSlug string
	PRNumber int
	CommitID string
	Path     string
	Line     int
	Side     string
	Body     string
}

// PostComment posts a single inline review comment via the REST API.
// Side is chosen by the caller from the parsed diff (RIGHT for added /
// context lines, LEFT for removed ones); a finding whose line is outside
// the diff is filtered out upstream and never reaches here, so a 422 is
// now an unexpected GitHub error rather than the routine hallucinated-line
// case (ADR-0016 §9).
func PostComment(ctx context.Context, r Runner, c CommentRequest) error {
	side := c.Side
	if side == "" {
		side = "RIGHT"
	}
	endpoint := fmt.Sprintf("/repos/%s/pulls/%d/comments", c.RepoSlug, c.PRNumber)
	_, err := r.Run(ctx,
		"api", "--method", "POST",
		"-H", "Accept: application/vnd.github+json",
		"-H", "X-GitHub-Api-Version: 2022-11-28",
		endpoint,
		"-f", "body="+c.Body,
		"-f", "commit_id="+c.CommitID,
		"-f", "path="+c.Path,
		"-F", "line="+strconv.Itoa(c.Line),
		"-f", "side="+side,
	)
	return err
}

// unanchoredHeading introduces findings that could not be attached to a
// diff line (the LLM referenced a line outside the diff, or the POST was
// rejected). They are appended to the review summary so the signal is
// not silently lost (ADR-0016 §9). Fixed English like the rest of the
// GitHub-posted text (ADR-0016 §10).
const unanchoredHeading = "Findings that could not be attached to a specific line:"

// BuildUnanchoredSection renders the findings that fell back to the
// review body. Returns "" when there are none so callers can append it
// unconditionally.
func BuildUnanchoredSection(findings []render.Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(unanchoredHeading)
	for _, f := range findings {
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "[%s] %s\n", strings.ToUpper(string(f.Severity)), f.PathRef())
		fmt.Fprintf(&b, "%s\n", f.Title)
		fmt.Fprintf(&b, "%s\n", f.Description)
		b.WriteString(f.Suggestion)
	}
	return b.String()
}
