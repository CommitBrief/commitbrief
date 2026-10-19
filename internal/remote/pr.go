// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// owner is the nested {"login": "..."} object gh emits for users and
// repository owners.
type owner struct {
	Login string `json:"login"`
}

// repoRef mirrors gh's headRepository shape.
type repoRef struct {
	Name  string `json:"name"`
	Owner owner  `json:"owner"`
}

// Slug returns "owner/name".
func (r repoRef) Slug() string { return r.Owner.Login + "/" + r.Name }

type commit struct {
	OID string `json:"oid"`
}

// PRMeta is the subset of `gh pr view --json ...` that remote pr consumes.
//
// Note: gh does not expose a baseRepository field on PRs — the base repo is
// derived from the PR URL (which always points at the base repo) via BaseSlug.
type PRMeta struct {
	Number         int      `json:"number"`
	Author         owner    `json:"author"`
	URL            string   `json:"url"`
	HeadRepository repoRef  `json:"headRepository"`
	Commits        []commit `json:"commits"`
}

// BaseSlug returns the base repository as "owner/name", parsed from the PR
// URL (https://<host>/<owner>/<repo>/pull/<n>). A PR's web URL always lives on
// the base repo, so this is correct even for cross-fork PRs. Returns "" if the
// URL is malformed.
func (m PRMeta) BaseSlug() string {
	u := m.URL
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	// parts: [host, owner, repo, "pull", n]
	parts := strings.Split(strings.TrimPrefix(u, "/"), "/")
	if len(parts) >= 3 {
		return parts[1] + "/" + parts[2]
	}
	return ""
}

// LastOID returns the head commit OID, or "" when the PR has no commits.
func (m PRMeta) LastOID() string {
	if len(m.Commits) == 0 {
		return ""
	}
	return m.Commits[len(m.Commits)-1].OID
}

// AuthorLogin returns the PR author's GitHub login.
func (m PRMeta) AuthorLogin() string { return m.Author.Login }

const prViewFields = "number,author,url,headRepository,commits"

// repoArgs appends `--repo owner/repo` when repo is non-empty.
func repoArgs(repo string, args ...string) []string {
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	return args
}

// FetchPRMeta runs `gh pr view <id> --json number,author,url,headRepository,commits`.
func FetchPRMeta(ctx context.Context, r Runner, id, repo string) (PRMeta, error) {
	var m PRMeta
	err := runJSON(ctx, r, &m, repoArgs(repo, "pr", "view", id, "--json", prViewFields)...)
	return m, err
}

// FetchLastOID re-reads only the commits to detect a head change cheaply
// between the diff fetch and the review submission (race check).
func FetchLastOID(ctx context.Context, r Runner, id, repo string) (string, error) {
	var m PRMeta
	if err := runJSON(ctx, r, &m, repoArgs(repo, "pr", "view", id, "--json", "commits")...); err != nil {
		return "", err
	}
	return m.LastOID(), nil
}

// Whoami returns the authenticated GitHub login (`gh api user -q .login`).
func Whoami(ctx context.Context, r Runner) (string, error) {
	out, err := r.Run(ctx, "api", "user", "-q", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// FetchDiff returns the PR's unified diff (`gh pr diff <id>`).
func FetchDiff(ctx context.Context, r Runner, id, repo string) (string, error) {
	out, err := r.Run(ctx, repoArgs(repo, "pr", "diff", id)...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

const reviewsQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      reviews(last:100){nodes{body viewerDidAuthor}}
    }
  }
}`

type gqlReviews struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data struct {
		Repository struct {
			PullRequest *struct {
				Reviews struct {
					Nodes []struct {
						Body            string `json:"body"`
						ViewerDidAuthor bool   `json:"viewerDidAuthor"`
					} `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// FetchLastReviewedOID returns the head OID recorded in the newest review the
// viewer authored that carries the K8 marker, looking at the PR's last 100
// reviews only. An empty OID with a nil error means there is none.
func FetchLastReviewedOID(ctx context.Context, r Runner, slug string, number int) (string, error) {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok || owner == "" || name == "" {
		return "", fmt.Errorf("invalid repo slug %q", slug)
	}
	out, err := r.Run(ctx,
		"api", "graphql",
		"-f", "query="+reviewsQuery,
		"-f", "owner="+owner,
		"-f", "name="+name,
		"-F", "number="+strconv.Itoa(number),
	)
	if err != nil {
		return "", err
	}
	var page gqlReviews
	if err := json.Unmarshal(out, &page); err != nil {
		return "", fmt.Errorf("decode reviews: %w", err)
	}
	if len(page.Errors) > 0 {
		return "", fmt.Errorf("reviews: %s", page.Errors[0].Message)
	}
	pr := page.Data.Repository.PullRequest
	if pr == nil {
		return "", errors.New("reviews: pull request not found")
	}
	// Nodes are oldest first: scan from the end for the newest match.
	nodes := pr.Reviews.Nodes
	for i := len(nodes) - 1; i >= 0; i-- {
		if !nodes[i].ViewerDidAuthor {
			continue
		}
		if oid, ok := ParseReviewedMarker(nodes[i].Body); ok {
			return oid, nil
		}
	}
	return "", nil
}

// Compare is the part of GET /repos/{o}/{r}/compare/{base}...{head} that the
// incremental review needs.
type Compare struct {
	Status       string          `json:"status"`
	TotalCommits int             `json:"total_commits"`
	Commits      []CompareCommit `json:"commits"`
}

// CompareCommit is one commit of a comparison; Parents has more than one
// entry for a merge commit.
type CompareCommit struct {
	SHA     string `json:"sha"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// Ahead reports whether head is strictly ahead of base, i.e. base is an
// ancestor of head (no force-push in between).
func (c Compare) Ahead() bool { return c.Status == "ahead" }

// HasMerge reports whether any compared commit is a merge commit.
func (c Compare) HasMerge() bool {
	for _, cm := range c.Commits {
		if len(cm.Parents) > 1 {
			return true
		}
	}
	return false
}

// Complete reports whether Commits lists every compared commit. The API caps
// the list; an incomplete one cannot prove the absence of merge commits.
func (c Compare) Complete() bool { return c.TotalCommits <= len(c.Commits) }

func comparePath(slug, base, head string) string {
	return "repos/" + slug + "/compare/" + base + "..." + head
}

// FetchCompare compares two commits through the REST API. The commit list is
// paginated by GitHub, so every page is read and merged.
func FetchCompare(ctx context.Context, r Runner, slug, base, head string) (Compare, error) {
	out, err := r.Run(ctx, "api", "--paginate", comparePath(slug, base, head)+"?per_page=100")
	if err != nil {
		return Compare{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var merged Compare
	for first := true; ; first = false {
		var page Compare
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) && !first {
				break
			}
			return Compare{}, fmt.Errorf("decode compare: %w", err)
		}
		if first {
			merged.Status = page.Status
			merged.TotalCommits = page.TotalCommits
		}
		merged.Commits = append(merged.Commits, page.Commits...)
	}
	return merged, nil
}

// FetchCompareDiff returns the unified diff of base...head. Large comparisons
// make the API fail; the caller then falls back to the full PR diff.
func FetchCompareDiff(ctx context.Context, r Runner, slug, base, head string) (string, error) {
	out, err := r.Run(ctx, "api", "-H", "Accept: application/vnd.github.diff", comparePath(slug, base, head))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
