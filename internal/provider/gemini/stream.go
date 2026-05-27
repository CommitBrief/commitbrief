// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"context"
	"iter"

	sdk "google.golang.org/genai"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func adaptStream(ctx context.Context, stream iter.Seq2[*sdk.GenerateContentResponse, error]) <-chan provider.Event {
	out := make(chan provider.Event, 16)
	go func() {
		defer close(out)

		emit := func(e provider.Event) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		var (
			input  int32
			output int32
			cached int32
		)

		stream(func(resp *sdk.GenerateContentResponse, err error) bool {
			if err != nil {
				_ = emit(provider.Event{Type: provider.EventError, Err: mapError(err)})
				return false
			}
			if resp == nil {
				return true
			}
			if text := resp.Text(); text != "" {
				if !emit(provider.Event{Type: provider.EventDelta, Delta: text}) {
					return false
				}
			}
			if u := resp.UsageMetadata; u != nil {
				// Gemini sends cumulative totals on each chunk; replace rather
				// than add so the final emit reflects the true totals.
				if u.PromptTokenCount > 0 {
					input = u.PromptTokenCount
				}
				if u.CandidatesTokenCount > 0 {
					output = u.CandidatesTokenCount
				}
				if u.CachedContentTokenCount > 0 {
					cached = u.CachedContentTokenCount
				}
			}
			return true
		})

		// Iterator finishing without error implies the stream completed.
		// We always emit usage + done; if the stream errored, we already
		// emitted EventError above and returned early via the false return.
		if ctx.Err() == nil {
			_ = emit(provider.Event{
				Type: provider.EventUsage,
				Usage: provider.Usage{
					InputTokens:       int(input),
					OutputTokens:      int(output),
					CachedInputTokens: int(cached),
				},
			})
			_ = emit(provider.Event{Type: provider.EventDone})
		}
	}()
	return out
}
