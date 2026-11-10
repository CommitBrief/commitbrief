// SPDX-License-Identifier: GPL-3.0-or-later

package render

// RemoteReport is the optional top-level `remote` container of the JSON v1
// document. It is present only in the document `remote pr <ID> --json` writes
// in posting mode (ADR-0048 §2); every other producer leaves Payload.Remote
// nil, so the v1 shape is byte-for-byte unchanged for them and `schema` stays 1.
//
// Evolution follows the v1 rules (ADR-0048 §3): new optional fields may be
// added in a minor release; renaming, removing, retyping or changing the
// meaning of a field requires schema 2 for the whole envelope.
type RemoteReport struct {
	Status          string          `json:"status"`
	PR              RemotePR        `json:"pr"`
	HeadOID         string          `json:"head_oid"`
	Verdict         string          `json:"verdict,omitempty"`
	VerdictUncapped string          `json:"verdict_uncapped,omitempty"`
	MaxVerdict      string          `json:"max_verdict"`
	Review          RemoteReview    `json:"review"`
	Sync            *RemoteSync     `json:"sync,omitempty"`
	Warnings        []RemoteWarning `json:"warnings"`
}

// RemotePR identifies the pull request the review was addressed to. Repo is
// the base repository "owner/name".
type RemotePR struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// RemoteReview describes how the review was produced.
type RemoteReview struct {
	Mode          string `json:"mode"`
	FullReason    string `json:"full_reason,omitempty"`
	PreviousOID   string `json:"previous_oid,omitempty"`
	Attempts      int    `json:"attempts"`
	MarkerWritten bool   `json:"marker_written"`
}

// RemoteSync carries the counters of the `Synced:` stderr line plus the two
// failure-shaped counts (ADR-0048 §2).
type RemoteSync struct {
	Mode       string `json:"mode"`
	Posted     int    `json:"posted"`
	Updated    int    `json:"updated"`
	Unchanged  int    `json:"unchanged"`
	Resolved   int    `json:"resolved"`
	Replied    int    `json:"replied"`
	Held       int    `json:"held"`
	PostFailed int    `json:"post_failed"`
	Unanchored int    `json:"unanchored"`
}

// RemoteWarning is one machine-readable condition of the run. Code is a
// stable, English, snake_case open set; Detail is raw, non-localized
// diagnostic text and is not stable.
type RemoteWarning struct {
	Code   string `json:"code"`
	Op     string `json:"op,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Count  int    `json:"count,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Remote report enum values (ADR-0048 §2, §3).
const (
	RemoteStatusSubmitted    = "submitted"
	RemoteStatusNoNewCommits = "no_new_commits"
	RemoteStatusDegraded     = "degraded"
	RemoteStatusTooVolatile  = "too_volatile"
	RemoteStatusFailed       = "failed"

	RemoteModeFull        = "full"
	RemoteModeIncremental = "incremental"
	RemoteModeNone        = "none"

	RemoteSyncSynced   = "synced"
	RemoteSyncPostOnly = "post_only"
	RemoteSyncSkipped  = "skipped"
)
