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
// head OID the diff was fetched at.
type CommentRequest struct {
	RepoSlug string
	PRNumber int
	CommitID string
	Path     string
	Line     int
	Body     string
}

// PostComment posts a single inline review comment via the REST API.
// `side=RIGHT` is unconditional (the LLM reviews newly-added code); a
// finding pinned to a deleted line may return 422, which the caller
// swallows per-comment (ADR-0016 §9).
func PostComment(ctx context.Context, r Runner, c CommentRequest) error {
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
		"-f", "side=RIGHT",
	)
	return err
}
