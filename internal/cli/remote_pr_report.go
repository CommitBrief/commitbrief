// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// Verdict-ceiling values for `remote pr --max-verdict` (ADR-0048 §4).
const (
	maxVerdictApprove = "approve"
	maxVerdictComment = "comment"
)

var (
	errMaxVerdictRequestChanges = errors.New("max-verdict: request-changes is not a ceiling")
	errMaxVerdictInvalid        = errors.New("max-verdict: invalid value")
)

// parseMaxVerdict validates --max-verdict. The empty string is the default
// (no ceiling, same as "approve"). `request-changes` is rejected with its own
// error because it is not a ceiling: it is enabled by --request-changes-on.
func parseMaxVerdict(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", maxVerdictApprove:
		return maxVerdictApprove, nil
	case maxVerdictComment:
		return maxVerdictComment, nil
	case "request-changes":
		return "", errMaxVerdictRequestChanges
	}
	return "", errMaxVerdictInvalid
}

// applyVerdictCeiling is the last step of the verdict pipeline (ADR-0048 §5):
// under --max-verdict comment an approve becomes a comment. request-changes
// is never touched; it is governed only by --request-changes-on.
func applyVerdictCeiling(v remote.Verdict, ceiling string) remote.Verdict {
	if ceiling == maxVerdictComment && v == remote.VerdictApprove {
		return remote.VerdictComment
	}
	return v
}

// verdictName is the report's enum spelling of a verdict.
func verdictName(v remote.Verdict) string {
	switch v {
	case remote.VerdictApprove:
		return "approve"
	case remote.VerdictRequestChanges:
		return "request-changes"
	default:
		return "comment"
	}
}

// Full-review reasons (remote.review.full_reason, ADR-0048 §2). Each one but
// the first and the last maps to a `remote.since_*` stderr note.
const (
	fullReasonNotRequested  = "not_requested"
	fullReasonNoMarker      = "no_marker"
	fullReasonLookupFailed  = "marker_lookup_failed"
	fullReasonCompareFailed = "compare_failed"
	fullReasonNotAhead      = "not_ahead"
	fullReasonIncomplete    = "compare_incomplete"
	fullReasonMergeCommit   = "merge_commit"
	fullReasonDiffFailed    = "diff_failed"
	// fullReasonEmptyDiff: --since-last-review found a marker, but the PR's
	// filtered diff was empty, so the incremental range was never evaluated.
	// It is an addition to the open full_reason set (ADR-0048 §3).
	fullReasonEmptyDiff = "empty_diff"
)

// prStatusError tags an error with the `remote.status` it ends the run with
// (degraded / too_volatile). The wrapped error is returned unchanged, so the
// stderr text and exit code are what they always were.
type prStatusError struct {
	status string
	err    error
}

func (e *prStatusError) Error() string { return e.err.Error() }
func (e *prStatusError) Unwrap() error { return e.err }

// prRun is the per-invocation accumulator of a posting-mode `remote pr` run.
// It is the single source for the stderr footer and the JSON `meta` (ADR-0048
// §2.1, "one sum, two renderings"): every provider call that returned usage is
// added here, including a first attempt the head-race retry superseded.
type prRun struct {
	usage   provider.Usage
	latency time.Duration
	// calls counts provider calls that returned usage. Once it is above zero
	// every way the run ends writes the document (ADR-0048 §2.1).
	calls    int
	attempts int
	retries  int

	headOID    string
	suppressed int

	// degradedContent / degradeReason are set when a provider answer could not
	// be parsed into findings.
	degradedContent string
	degradeReason   string

	// Review mode of the latest attempt that was started.
	mode        string
	fullReason  string
	baseReason  string // full_reason when the run is not incremental and nothing more specific applies
	previousOID string

	findings []render.Finding

	// verdictSet is needed because the zero remote.Verdict is approve.
	verdictSet    bool
	verdict       remote.Verdict
	verdictUncap  remote.Verdict
	markerWritten bool
	sync          *render.RemoteSync

	warnings []render.RemoteWarning
}

// addCall folds one returned provider call into the run's totals.
func (r *prRun) addCall(u provider.Usage, d time.Duration, retries int) {
	r.usage.InputTokens += u.InputTokens
	r.usage.OutputTokens += u.OutputTokens
	r.usage.CachedInputTokens += u.CachedInputTokens
	r.latency += d
	r.retries += retries
	r.calls++
}

func (r *prRun) warn(w render.RemoteWarning) {
	r.warnings = append(r.warnings, w)
}

// setSecretHits records the secret-scanner warning once. A head-race retry
// re-scans; the count of the latest attempt that flagged anything replaces an
// earlier one. It is only called on a hit, so an attempt that found nothing
// leaves the earlier count in place: that diff did reach the provider.
func (r *prRun) setSecretHits(n int) {
	for i := range r.warnings {
		if r.warnings[i].Code == "secrets_detected" {
			r.warnings[i].Count = n
			return
		}
	}
	r.warn(render.RemoteWarning{Code: "secrets_detected", Count: n})
}

// setReview records how the current attempt reviews the PR.
func (r *prRun) setReview(mode, fullReason, previousOID string) {
	r.mode, r.fullReason, r.previousOID = mode, fullReason, previousOID
}

// recordSyncWarning maps one remote.Warning to its report entry, mirroring
// syncWarning's three stderr shapes.
func (r *prRun) recordSyncWarning(w remote.Warning) {
	switch {
	case w.Op == "post" && w.Err != nil:
		r.warn(render.RemoteWarning{Code: "comment_post_failed", Ref: w.Ref, Detail: w.Err.Error()})
	case w.Err == nil:
		r.warn(render.RemoteWarning{Code: "sync_not_permitted", Op: w.Op, Ref: w.Ref})
	default:
		r.warn(render.RemoteWarning{Code: "sync_op_failed", Op: w.Op, Ref: w.Ref, Detail: w.Err.Error()})
	}
}

// statusFromError classifies the error a run ended with.
func statusFromError(err error) string {
	var se *prStatusError
	if errors.As(err, &se) {
		return se.status
	}
	return render.RemoteStatusFailed
}

// buildRemoteReport assembles the `remote` container for the given ending.
func (r *prRun) buildRemoteReport(status, repo string, number int, ceiling string) *render.RemoteReport {
	rep := &render.RemoteReport{
		Status:     status,
		PR:         render.RemotePR{Repo: repo, Number: number},
		HeadOID:    r.headOID,
		MaxVerdict: ceiling,
		Review: render.RemoteReview{
			Mode:          r.mode,
			FullReason:    r.fullReason,
			PreviousOID:   r.previousOID,
			Attempts:      r.attempts,
			MarkerWritten: r.markerWritten,
		},
		Sync:     r.sync,
		Warnings: r.warnings,
	}
	if r.verdictSet {
		rep.Verdict = verdictName(r.verdict)
		rep.VerdictUncapped = verdictName(r.verdictUncap)
	}
	if rep.Review.Mode == "" {
		rep.Review.Mode = render.RemoteModeNone
	}
	if rep.Review.Mode != render.RemoteModeFull {
		rep.Review.FullReason = ""
	}
	return rep
}

// reportPayload builds the JSON v1 payload for the run: the envelope carries
// the summed meta, the post-suppression findings and, for a degraded run, the
// raw model text.
func (r *prRun) reportPayload(status string, meta render.Meta, remoteRep *render.RemoteReport) render.Payload {
	meta.Retries = r.retries
	meta.Suppressed = r.suppressed
	meta.DegradeReason = r.degradeReason
	p := render.Payload{Meta: meta, Remote: remoteRep}
	switch status {
	case render.RemoteStatusDegraded:
		p.Content = r.degradedContent // Findings stays nil: graceful degrade
	default:
		p.Findings = r.findings
		if p.Findings == nil {
			p.Findings = []render.Finding{}
		}
	}
	return p
}

// insecureHTTPHost reports whether the provider's configured base_url is a
// plain-http, non-loopback endpoint, and its host. It is the single predicate
// behind both the provider.insecure_http_note stderr line and the
// insecure_http report warning.
func insecureHTTPHost(app *appContext, name string) (string, bool) {
	m, ok := provider.MetadataFor(name)
	if !ok || !m.PricingUserDeclared {
		return "", false
	}
	return insecureRemoteBaseURL(app.Config.Providers[name].BaseURL)
}

// reportSink is the --output destination of the posting-mode report. It is
// opened in preflight, before anything is written to GitHub, so an unwritable
// path fails the run while it is still safe to retry: opening it after the
// review was submitted would turn a submitted review into "exit 1, empty
// stdout", which a consumer reads as "nothing happened" (ADR-0048 §2.1). The
// open does not truncate (so a failed preflight elsewhere never destroys a
// file), but a run that ends without writing a document empties an existing
// file, and removes one the open created: a stale document from an earlier run
// must never be readable as this run's result, and "empty file" has to mean
// what "empty stdout" means.
type reportSink struct {
	f       *os.File
	created bool
}

func openReportSink(path string) (*reportSink, error) {
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644) // #nosec G302 G304 -- user-chosen --output path, same mode os.Create gives
	if err != nil {
		return nil, fmt.Errorf("open --output: %w", err)
	}
	return &reportSink{f: f, created: errors.Is(statErr, os.ErrNotExist)}, nil
}

// write replaces the file's content with the document.
func (k *reportSink) write(p render.Payload) error {
	if err := k.f.Truncate(0); err != nil {
		return err
	}
	if _, err := k.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return render.JSON(k.f, p)
}

// close releases the file. When no document was written it truncates an
// existing file to zero bytes, or removes a file the preflight created.
func (k *reportSink) close(wrote bool) {
	if !wrote && !k.created {
		_ = k.f.Truncate(0)
	}
	_ = k.f.Close()
	if !wrote && k.created {
		_ = os.Remove(k.f.Name())
	}
}

// writeRemoteReport renders the JSON document to the sink (--output) or, with
// no sink, to stdout.
func writeRemoteReport(cmd *cobra.Command, sink *reportSink, p render.Payload) error {
	if sink != nil {
		return sink.write(p)
	}
	return render.JSON(cmd.OutOrStdout(), p)
}
