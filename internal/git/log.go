// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CommitMeta is one commit's human-relevant metadata, used by the
// `commitbrief summary` command to attribute logical changes to the
// commit(s) that introduced them. It carries no diff body — the cumulative
// range diff is fetched separately via the Diff() passthrough — only the
// short hash, the author's subject/body (the "commit message" the summary
// is asked to take into account), and the paths the commit touched (so the
// model can map a logical area back to the commit responsible for it).
type CommitMeta struct {
	Short   string   // abbreviated hash, e.g. "a1b2c3d"
	Subject string   // first line of the commit message
	Body    string   // remainder of the commit message (may be empty)
	Files   []string // post-change paths touched by this commit
}

// maxRangeCommits bounds how many commits RangeCommits feeds into a summary
// prompt. A range like main..develop can carry hundreds of commits; past a
// few dozen the manifest stops improving attribution and just burns input
// tokens. We keep the most recent maxRangeCommits (git log's default,
// newest-first order) and let the cumulative diff carry the rest.
const maxRangeCommits = 50

// Record/field separators chosen from the ASCII control range (RS / US) so
// they never collide with real commit message or path content. The log
// format emits one RS-prefixed record per commit; within a record the
// short hash, subject, body, and the trailing --name-status block are
// US-separated.
const (
	logRecordSep = "\x1e"
	logFieldSep  = "\x1f"
)

// rangeCommitFormat pins the per-commit layout: <RS><short><US><subject><US><body><US>.
// The trailing US closes the format so the --name-status lines git appends
// after it land in their own field when the record is split.
const rangeCommitFormat = "--format=" + logRecordSep + "%h" + logFieldSep + "%s" + logFieldSep + "%b" + logFieldSep

// RangeCommits returns the commit manifest for a log range (e.g.
// {"main..develop"} or {"HEAD~3..HEAD"}). It is read-only — `git log` only —
// and the caller is expected to have already derived a clean two-endpoint
// range from the user's diff arguments (see deriveLogRange in the cli
// package); arbitrary single refs would make `git log` walk all of history.
//
// Merge commits are excluded (--no-merges): their diffs are already covered
// by the cumulative range diff and their subjects ("Merge branch …") add
// noise rather than intent. The result is capped at maxRangeCommits.
//
// Best-effort by contract: a non-zero git exit (bad range, shallow clone,
// etc.) returns an error the caller may treat as "no manifest" and degrade
// to a diff-only summary rather than failing the whole command.
func RangeCommits(ctx context.Context, repoRoot string, logArgs []string) ([]CommitMeta, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoGitCLI
	}
	args := []string{
		"log",
		"--no-color",
		"--no-merges",
		fmt.Sprintf("-n%d", maxRangeCommits),
		"--name-status",
		rangeCommitFormat,
	}
	args = append(args, logArgs...)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git log %s: %s", strings.Join(logArgs, " "), msg)
	}
	return parseRangeCommits(stdout.String()), nil
}

// parseRangeCommits turns the RS/US-delimited `git log` output into
// CommitMeta records. Split on RS yields one block per commit (the first
// split element, before the first RS, is empty boilerplate); each block
// splits on US into [short, subject, body, fileBlock]. Malformed blocks are
// skipped rather than aborting the whole parse — the manifest is advisory.
func parseRangeCommits(out string) []CommitMeta {
	blocks := strings.Split(out, logRecordSep)
	commits := make([]CommitMeta, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		fields := strings.SplitN(block, logFieldSep, 4)
		if len(fields) < 3 {
			continue
		}
		short := strings.TrimSpace(fields[0])
		if short == "" {
			continue
		}
		c := CommitMeta{
			Short:   short,
			Subject: strings.TrimSpace(fields[1]),
			Body:    strings.TrimSpace(fields[2]),
		}
		if len(fields) == 4 {
			c.Files = parseNameStatus(fields[3])
		}
		commits = append(commits, c)
	}
	return commits
}

// parseNameStatus extracts the resulting path from each `git log
// --name-status` line. Status lines are TAB-separated: "M\tpath",
// "A\tpath", "D\tpath", and renames/copies "R100\told\tnew" /
// "C75\tsrc\tdst". The resulting path is always the LAST tab-separated
// field, so we take that uniformly across every status code.
func parseNameStatus(block string) []string {
	var files []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		path := strings.TrimSpace(parts[len(parts)-1])
		if path != "" {
			files = append(files, path)
		}
	}
	return files
}
