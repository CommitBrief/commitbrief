// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"strings"
)

// owner is the nested {"login": "..."} object gh emits for users and
// repository owners.
type owner struct {
	Login string `json:"login"`
}

// repoRef mirrors gh's baseRepository / headRepository shape.
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
type PRMeta struct {
	Number         int      `json:"number"`
	Author         owner    `json:"author"`
	BaseRepository repoRef  `json:"baseRepository"`
	HeadRepository repoRef  `json:"headRepository"`
	Commits        []commit `json:"commits"`
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

const prViewFields = "number,author,baseRepository,headRepository,commits"

// repoArgs appends `--repo owner/repo` when repo is non-empty.
func repoArgs(repo string, args ...string) []string {
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	return args
}

// FetchPRMeta runs `gh pr view <id> --json number,author,baseRepository,headRepository,commits`.
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
