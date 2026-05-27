// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 80 * time.Millisecond

type Spinner struct {
	w        io.Writer
	enabled  bool
	mu       sync.Mutex
	message  string
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
	active   atomic.Bool
}

// NewSpinner returns a spinner that animates frames in w. When the writer is
// not a TTY (or NO_COLOR-equivalent is set), the spinner is inert: Start does
// nothing, Stop does nothing. Callers don't need to branch on TTY state.
func NewSpinner(w io.Writer, message string) *Spinner {
	return &Spinner{
		w:       w,
		enabled: isTerminal(w),
		message: message,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *Spinner) Start() {
	if !s.enabled || s.active.Swap(true) {
		return
	}
	go s.run()
}

func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	s.message = msg
	s.mu.Unlock()
}

func (s *Spinner) Stop() {
	if !s.enabled {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
		// Clear the spinner line so subsequent output starts clean.
		_, _ = fmt.Fprint(s.w, "\r\033[K")
	})
}

func (s *Spinner) run() {
	defer close(s.done)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	frame := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			msg := s.message
			s.mu.Unlock()
			_, _ = fmt.Fprintf(s.w, "\r%s %s\033[K", spinnerFrames[frame%len(spinnerFrames)], msg)
			frame++
		}
	}
}
