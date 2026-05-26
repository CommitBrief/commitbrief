package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// StreamResult is the accumulated outcome of draining a provider stream.
// Content is the full assembled response; Usage is the last reported usage
// (may be the cumulative final value); Err is non-nil on a stream error.
type StreamResult struct {
	Content string
	Usage   provider.Usage
	Err     error
}

// Drain consumes events from ch and writes deltas to w as they arrive.
// Returns when the channel closes, the context is done, or an error event
// is observed. Safe to call with a nil writer (deltas are still buffered
// in the returned StreamResult.Content).
func Drain(ctx context.Context, ch <-chan provider.Event, w io.Writer) StreamResult {
	var (
		buf  strings.Builder
		last provider.Usage
	)
	for {
		select {
		case <-ctx.Done():
			return StreamResult{Content: buf.String(), Usage: last, Err: ctx.Err()}
		case ev, ok := <-ch:
			if !ok {
				return StreamResult{Content: buf.String(), Usage: last}
			}
			switch ev.Type {
			case provider.EventDelta:
				buf.WriteString(ev.Delta)
				if w != nil && ev.Delta != "" {
					_, _ = fmt.Fprint(w, ev.Delta)
				}
			case provider.EventUsage:
				last = ev.Usage
			case provider.EventError:
				return StreamResult{Content: buf.String(), Usage: last, Err: ev.Err}
			case provider.EventDone:
				// keep draining in case usage / done order differs across providers
			}
		}
	}
}
