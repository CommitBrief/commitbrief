// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func adaptStream(ctx context.Context, stream *ssestream.Stream[sdk.ChatCompletionChunk]) <-chan provider.Event {
	out := make(chan provider.Event, 16)
	go func() {
		defer close(out)
		defer func() { _ = stream.Close() }()

		var (
			input  int64
			output int64
			cached int64
		)

		emit := func(e provider.Event) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for stream.Next() {
			chunk := stream.Current()
			// OpenAI streams: each chunk has Choices[0].Delta.Content with
			// the next text fragment; the LAST chunk (when include_usage=true)
			// carries the total Usage and an empty Choices slice.
			if len(chunk.Choices) > 0 {
				if delta := chunk.Choices[0].Delta.Content; delta != "" {
					if !emit(provider.Event{Type: provider.EventDelta, Delta: delta}) {
						return
					}
				}
			}
			if chunk.Usage.TotalTokens > 0 {
				input = chunk.Usage.PromptTokens
				output = chunk.Usage.CompletionTokens
				cached = chunk.Usage.PromptTokensDetails.CachedTokens
			}
		}

		if err := stream.Err(); err != nil {
			_ = emit(provider.Event{Type: provider.EventError, Err: mapError(err)})
			return
		}

		_ = emit(provider.Event{
			Type: provider.EventUsage,
			Usage: provider.Usage{
				InputTokens:       int(input),
				OutputTokens:      int(output),
				CachedInputTokens: int(cached),
			},
		})
		_ = emit(provider.Event{Type: provider.EventDone})
	}()
	return out
}
