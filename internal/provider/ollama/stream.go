// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// adaptStream reads Ollama's newline-delimited JSON stream and forwards
// each chunk's text content as an EventDelta. The final line carries
// `"done": true` plus the final token counts; we emit EventUsage and
// EventDone from there.
func adaptStream(ctx context.Context, body io.ReadCloser) <-chan provider.Event {
	out := make(chan provider.Event, 16)
	go func() {
		defer close(out)
		defer func() { _ = body.Close() }()

		emit := func(e provider.Event) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		scanner := bufio.NewScanner(body)
		// 1MB max per line — Ollama can emit large final-summary lines on
		// long generations. The default scanner buffer (64KB) is too small.
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		var (
			lastInput  int
			lastOutput int
		)

		for scanner.Scan() {
			var chunk chatResponse
			if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
				_ = emit(provider.Event{
					Type: provider.EventError,
					Err:  fmt.Errorf("ollama: decode stream chunk: %w", err),
				})
				return
			}
			if delta := chunk.Message.Content; delta != "" {
				if !emit(provider.Event{Type: provider.EventDelta, Delta: delta}) {
					return
				}
			}
			if chunk.Done {
				if chunk.PromptEvalCount > 0 {
					lastInput = chunk.PromptEvalCount
				}
				if chunk.EvalCount > 0 {
					lastOutput = chunk.EvalCount
				}
				_ = emit(provider.Event{
					Type: provider.EventUsage,
					Usage: provider.Usage{
						InputTokens:  lastInput,
						OutputTokens: lastOutput,
					},
				})
				_ = emit(provider.Event{Type: provider.EventDone})
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = emit(provider.Event{
				Type: provider.EventError,
				Err:  fmt.Errorf("ollama: stream scan: %w", err),
			})
		}
	}()
	return out
}
