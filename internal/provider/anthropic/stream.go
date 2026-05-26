package anthropic

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func adaptStream(ctx context.Context, stream *ssestream.Stream[sdk.MessageStreamEventUnion]) <-chan provider.Event {
	out := make(chan provider.Event, 16)
	go func() {
		defer close(out)
		defer func() { _ = stream.Close() }()

		var (
			input        int64
			output       int64
			cacheRead    int64
			cacheCreate  int64
			usageEmitted bool
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
			ev := stream.Current()
			switch ev.Type {
			case "content_block_delta":
				if delta := ev.Delta.Text; delta != "" {
					if !emit(provider.Event{Type: provider.EventDelta, Delta: delta}) {
						return
					}
				}
			case "message_start":
				u := ev.Message.Usage
				input += u.InputTokens
				output += u.OutputTokens
				cacheRead += u.CacheReadInputTokens
				cacheCreate += u.CacheCreationInputTokens
			case "message_delta":
				// Anthropic emits cumulative totals on message_delta; replace
				// the running counters rather than adding to avoid double-counting.
				u := ev.Usage
				if u.InputTokens > 0 {
					input = u.InputTokens
				}
				if u.OutputTokens > 0 {
					output = u.OutputTokens
				}
				if u.CacheReadInputTokens > 0 {
					cacheRead = u.CacheReadInputTokens
				}
				if u.CacheCreationInputTokens > 0 {
					cacheCreate = u.CacheCreationInputTokens
				}
			case "message_stop":
				if !usageEmitted {
					if !emit(provider.Event{
						Type: provider.EventUsage,
						Usage: provider.Usage{
							InputTokens:       int(input + cacheRead + cacheCreate),
							OutputTokens:      int(output),
							CachedInputTokens: int(cacheRead),
						},
					}) {
						return
					}
					usageEmitted = true
				}
			}
		}

		if err := stream.Err(); err != nil {
			_ = emit(provider.Event{Type: provider.EventError, Err: mapError(err)})
			return
		}
		_ = emit(provider.Event{Type: provider.EventDone})
	}()
	return out
}
