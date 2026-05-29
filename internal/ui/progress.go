// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// Progress is the staged-spinner driving the review pipeline's
// long-running operations. It renders a `tree` of stage lines on a
// single writer (typically stderr), animating the leading `⏺` glyph
// on the *currently active* stage and freezing prior stages with a
// terminal-state color (green for Finish, red for Fail, muted for
// Soft).
//
// Three operating modes, decided at construction:
//
//   - **Animated** — TTY writer + colors enabled. Full breathing-dot
//     animation, ANSI cursor escapes to redraw the tree, multi-line
//     output. The natural mode for interactive terminals.
//   - **Plain** — non-TTY writer (CI, pipe) but not silent. One line
//     per stage transition (`[start]`, `[done]`, `[info]`, `[fail]`).
//     No animation. Useful for CI logs.
//   - **Silent** — caller opts out (Quiet=true). No output at all.
//
// The mode is fixed for the lifetime of the Progress; callers don't
// need to branch on TTY state at each call.
//
// Concurrency: Start/Info/Finish/Fail/Soft are safe to call from a
// single goroutine driving the pipeline. The animation lives in its
// own goroutine that reads the stage list under a mutex.
type Progress struct {
	w           io.Writer
	mode        progressMode
	mu          sync.Mutex
	stages      []stage
	stop        chan struct{}
	done        chan struct{}
	active      atomic.Bool
	closed      atomic.Bool
	paused      atomic.Bool
	frame       int
	prevLen     int       // number of stage lines drawn last render; tracked so we can `cursor up N` on next redraw
	width       int       // terminal columns; 0 = unknown (no truncation). Lines are clipped to this so they never wrap — a wrapped line would occupy more physical rows than prevLen counts, desyncing the cursor-up redraw and flooding the screen.
	activeSince time.Time // when the current active stage started; drives the live elapsed-time counter shown on slow stages (e.g. "Thinking… 0:42") so a long agent call reads as working, not frozen.
}

// stage is one entry in the tree.
type stage struct {
	label string
	state stageState
	err   error // populated only when state == stageFail
	info  bool  // true for static informational lines (no leading dot)
}

type stageState int

const (
	stageActive stageState = iota
	stageDone              // green ⏺
	stageFail              // red ⏺ + indented error
	stageSoft              // muted ⏺ — neutral terminal state (e.g. retry-soft)
	stageInfo              // static info line (no glyph)
)

type progressMode int

const (
	progressAnimated progressMode = iota
	progressPlain
	progressSilent
)

// NewProgress returns a Progress configured for the given writer. The
// Animated mode requires a TTY writer and colors enabled (ColorAuto +
// no NO_COLOR env, or ColorAlways). On non-TTY or NO_COLOR, falls
// back to Plain mode. Set Quiet=true to suppress every output mode.
func NewProgress(w io.Writer, mode ColorMode, quiet bool) *Progress {
	p := &Progress{w: w}
	switch {
	case quiet:
		p.mode = progressSilent
	case ColorEnabled(w, mode):
		p.mode = progressAnimated
		p.stop = make(chan struct{})
		p.done = make(chan struct{})
		// Capture terminal width so redraw can clip lines and never wrap.
		// Animated mode implies a TTY writer, so GetSize normally succeeds;
		// a failure leaves width 0 (clipping disabled — best-effort).
		if f, ok := w.(*os.File); ok {
			if cols, _, err := term.GetSize(int(f.Fd())); err == nil {
				p.width = cols
			}
		}
	default:
		p.mode = progressPlain
	}
	return p
}

// Start appends a new active stage, finalizing the previous stage
// with Done (green) state. If no animation goroutine is running yet,
// one is spawned.
func (p *Progress) Start(label string) {
	switch p.mode {
	case progressSilent:
		return
	case progressPlain:
		p.mu.Lock()
		var doneLabel string
		if n := len(p.stages); n > 0 && p.stages[n-1].state == stageActive {
			p.stages[n-1].state = stageDone
			doneLabel = p.stages[n-1].label
		}
		p.stages = append(p.stages, stage{label: label, state: stageActive})
		p.mu.Unlock()
		if doneLabel != "" {
			_, _ = fmt.Fprintln(p.w, "[done]  "+doneLabel)
		}
		_, _ = fmt.Fprintln(p.w, "[start] "+label)
		return
	}

	p.mu.Lock()
	// Auto-finish any currently-active stage as Done. Callers that
	// want a different terminal state (Fail/Soft) must call the
	// matching method before Start.
	if n := len(p.stages); n > 0 && p.stages[n-1].state == stageActive {
		p.stages[n-1].state = stageDone
	}
	p.stages = append(p.stages, stage{label: label, state: stageActive})
	p.activeSince = time.Now()
	p.mu.Unlock()

	p.kickAnimation()
}

// Info appends a static informational line (no animated glyph, no
// terminal state). Used for stats lines like "36 files +1233 -34".
func (p *Progress) Info(label string) {
	switch p.mode {
	case progressSilent:
		return
	case progressPlain:
		// An active stage above us is implicitly done now: the
		// pipeline has moved past it (the file-count info comes
		// after the diff parse, so "Searching..." is complete).
		// Auto-finish keeps plain output parallel with animated.
		p.mu.Lock()
		var doneLabel string
		if n := len(p.stages); n > 0 && p.stages[n-1].state == stageActive {
			p.stages[n-1].state = stageDone
			doneLabel = p.stages[n-1].label
		}
		p.stages = append(p.stages, stage{label: label, info: true, state: stageInfo})
		p.mu.Unlock()
		if doneLabel != "" {
			_, _ = fmt.Fprintln(p.w, "[done]  "+doneLabel)
		}
		_, _ = fmt.Fprintln(p.w, "[info]  "+label)
		return
	}

	p.mu.Lock()
	// Same auto-finish rule as Start: the active stage above us is
	// now done, this info line is unrelated below it.
	if n := len(p.stages); n > 0 && p.stages[n-1].state == stageActive {
		p.stages[n-1].state = stageDone
	}
	p.stages = append(p.stages, stage{label: label, info: true, state: stageInfo})
	p.mu.Unlock()
	p.redraw()
}

// Finish marks the current stage as completed (green ⏺).
func (p *Progress) Finish() { p.terminate(stageDone, nil, "done") }

// Fail marks the current stage as failed (red ⏺) and emits the error
// indented below it on the next render.
func (p *Progress) Fail(err error) { p.terminate(stageFail, err, "fail") }

// Soft marks the current stage as completed with a neutral muted dot.
// Used when the operation produced a result that wasn't an outright
// success but isn't an error either — e.g. the first attempt of a
// retry-once pipeline.
func (p *Progress) Soft() { p.terminate(stageSoft, nil, "soft") }

func (p *Progress) terminate(state stageState, err error, plainTag string) {
	switch p.mode {
	case progressSilent:
		return
	case progressPlain:
		p.mu.Lock()
		// Target the last active stage, skipping any trailing info
		// entries. If none is found, the call is a no-op (caller
		// already terminated the stage or never started one).
		idx := -1
		for i := len(p.stages) - 1; i >= 0; i-- {
			if p.stages[i].state == stageActive {
				idx = i
				break
			}
		}
		var label string
		if idx >= 0 {
			p.stages[idx].state = state
			p.stages[idx].err = err
			label = p.stages[idx].label
		}
		p.mu.Unlock()
		if label != "" {
			_, _ = fmt.Fprintln(p.w, "["+plainTag+"]  "+label)
			if err != nil {
				_, _ = fmt.Fprintln(p.w, "        "+err.Error())
			}
		}
		return
	}

	p.mu.Lock()
	for i := len(p.stages) - 1; i >= 0; i-- {
		if p.stages[i].state == stageActive {
			p.stages[i].state = state
			p.stages[i].err = err
			break
		}
	}
	p.mu.Unlock()
	p.redraw()
}

// Pause stops the animation loop and clears the in-progress render
// so the terminal is handed back to the caller (e.g. an interactive
// cost prompt). The recorded stage list is preserved; Resume
// continues from where it left off.
func (p *Progress) Pause() {
	if p.mode != progressAnimated {
		return
	}
	p.paused.Store(true)
	if p.active.Swap(false) {
		close(p.stop)
		<-p.done
		// Clear the rendered area so the prompt has clean ground.
		p.clearArea()
		p.stop = make(chan struct{})
		p.done = make(chan struct{})
	}
}

// Resume restarts the animation after Pause. No-op if not paused.
func (p *Progress) Resume() {
	if p.mode != progressAnimated || !p.paused.Swap(false) {
		return
	}
	p.kickAnimation()
}

// Close stops the animation goroutine, ensuring the final frame is
// rendered (so completed stages stay on screen) and the cursor is
// restored to a known column. Idempotent.
//
// Use Close when the progress tree is the user's primary feedback
// (empty-diff `no_changes` path, provider-error path). Use Clear
// instead when downstream output — rendered cards, JSON, markdown —
// will take over the screen and the tree would just be clutter.
func (p *Progress) Close() {
	if p.closed.Swap(true) {
		return
	}
	if p.mode != progressAnimated {
		return
	}
	if p.active.Swap(false) {
		close(p.stop)
		<-p.done
	}
	// Final render so the user sees the terminal state of every
	// stage; ensure cursor sits on a fresh line below the tree.
	p.frame = -1 // negative → renderFrame uses the final-frame color set
	p.redraw()
	_, _ = fmt.Fprint(p.w, "\n")
}

// Clear stops the animation and erases the rendered progress tree
// from the terminal. Use this when downstream output (cards / JSON /
// markdown body) is about to take over the screen and the per-stage
// breadcrumbs would be redundant clutter — the rendered output is
// itself proof the pipeline completed.
//
// In plain mode (non-TTY) Clear is a no-op: those per-stage lines
// already shipped on each Start/Info/Finish call, are useful in CI
// logs, and aren't visually redundant the way a fixed-viewport tree
// is. In silent mode there is nothing to erase. Idempotent.
func (p *Progress) Clear() {
	if p.closed.Swap(true) {
		return
	}
	if p.mode != progressAnimated {
		return
	}
	if p.active.Swap(false) {
		close(p.stop)
		<-p.done
	}
	p.clearArea()
}

// kickAnimation starts the ticker goroutine if no one is already
// driving it. Safe to call from Start/Resume.
func (p *Progress) kickAnimation() {
	if p.active.Swap(true) {
		return // already running
	}
	go p.run()
}

func (p *Progress) run() {
	defer close(p.done)
	ticker := time.NewTicker(progressFrameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.frame++
			p.redraw()
		}
	}
}

// redraw repaints the entire stage tree in place. Uses ANSI cursor-up
// to overwrite previous output. Caller holds no mutex.
func (p *Progress) redraw() {
	if p.mode != progressAnimated {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	var buf strings.Builder
	if p.prevLen > 0 {
		// Move cursor up to the first stage line + clear each line.
		fmt.Fprintf(&buf, "\r\033[%dA", p.prevLen)
	}
	frame := p.frame
	if frame < 0 {
		frame = 0 // final frame: use the "settled" appearance of the active dot
	}
	// Clip budgets so no rendered line wraps (see Progress.width). A label
	// line is "├─ ⏺ " (5 cols) + label; an error line is "   " (3 cols) +
	// text. A zero budget means "width unknown" → no clipping.
	labelBudget, errBudget := 0, 0
	if p.width > 5 {
		labelBudget = p.width - 5
	}
	if p.width > 3 {
		errBudget = p.width - 3
	}
	for i, st := range p.stages {
		isLast := i == len(p.stages)-1
		connector := "├─ "
		if isLast {
			connector = "└─ "
		}
		buf.WriteString(connector)
		switch st.state {
		case stageActive:
			buf.WriteString(animatedDotColor(frame))
		case stageDone:
			buf.WriteString(stageDoneDot)
		case stageFail:
			buf.WriteString(stageFailDot)
		case stageSoft:
			buf.WriteString(stageSoftDot)
		case stageInfo:
			buf.WriteString(stageInfoLeader)
		}
		buf.WriteString(" ")
		// Live elapsed-time counter on the active stage (only once it's run
		// long enough to be worth showing, so fast stages stay clean). Its
		// visible width is reserved out of the label budget so the line
		// still never wraps.
		budget := labelBudget
		var timer string
		if st.state == stageActive && p.frame >= 0 {
			if d := time.Since(p.activeSince); d >= elapsedShowAfter {
				timer = formatElapsed(d)
			}
		}
		if timer != "" && budget > 0 {
			if budget -= len([]rune(timer)) + 1; budget < 1 {
				budget = 1
			}
		}
		buf.WriteString(clip(st.label, budget))
		if timer != "" {
			buf.WriteString(" " + stageTimerColor + timer + "\033[0m")
		}
		buf.WriteString("\033[K\n") // clear to end-of-line + newline
		// Error line under a failed stage.
		if st.state == stageFail && st.err != nil {
			buf.WriteString("   ")
			buf.WriteString(stageFailErrorLine(clip(st.err.Error(), errBudget)))
			buf.WriteString("\033[K\n")
		}
		// Trunk separator between adjacent stages — gives each line
		// breathing room and visually completes the tree drawing
		// (the trunk descends through these blanks down to the next
		// branch). Skipped after the final stage so the cursor lands
		// cleanly below the tree.
		if !isLast {
			buf.WriteString("│\033[K\n")
		}
	}
	// prevLen accounting: every stage emits one line, every failed
	// stage adds an error line, and there's a separator line between
	// each adjacent pair of stages.
	p.prevLen = len(p.stages)
	if len(p.stages) > 1 {
		p.prevLen += len(p.stages) - 1
	}
	for _, st := range p.stages {
		if st.state == stageFail && st.err != nil {
			p.prevLen++
		}
	}
	_, _ = io.WriteString(p.w, buf.String())
}

// clearArea blanks the rendered area without rewriting it. Used by
// Pause so the prompt has a clean canvas.
func (p *Progress) clearArea() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prevLen == 0 {
		return
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "\r\033[%dA", p.prevLen)
	for i := 0; i < p.prevLen; i++ {
		buf.WriteString("\033[K\n")
	}
	fmt.Fprintf(&buf, "\033[%dA", p.prevLen) // cursor back to top
	_, _ = io.WriteString(p.w, buf.String())
	p.prevLen = 0
}

// --- Animation timing + palette ------------------------------------

const progressFrameInterval = 180 * time.Millisecond

// elapsedShowAfter is how long a stage must run before its live timer
// appears. Below this, fast stages (searching, preparing) stay clean
// instead of flickering "0s".
const elapsedShowAfter = time.Second

// stageTimerColor is the muted gray (#5b6273, ink-300) the elapsed-time
// counter renders in, so it sits quietly beside the active stage label.
const stageTimerColor = "\033[38;2;91;98;115m"

// formatElapsed renders a duration as "42s" under a minute, "1:23" beyond.
func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// breathing color stops, cards.go palette aligned. 6-frame cycle:
//
//	0: muted (#3a3f4f) → 1: mid-1 (#4f5563) → 2: mid-2 (#6b7280)
//	3: bright (#9CA3AF) → 4: mid-2 → 5: mid-1 → wraps back to 0.
var breathingColors = []string{
	"\033[38;2;58;63;79m",    // #3a3f4f
	"\033[38;2;79;85;99m",    // #4f5563
	"\033[38;2;107;114;128m", // #6b7280
	"\033[38;2;156;163;175m", // #9CA3AF
	"\033[38;2;107;114;128m",
	"\033[38;2;79;85;99m",
}

const (
	// Final-state dots reuse the card palette: green (cardAddFg),
	// red (cardDelFg), muted neutral.
	stageDoneDot    = "\033[38;2;34;211;160m⏺\033[0m"  // #22d3a0
	stageFailDot    = "\033[38;2;255;107;138m⏺\033[0m" // #ff6b8a
	stageSoftDot    = "\033[38;2;156;163;175m⏺\033[0m" // #9CA3AF
	stageInfoLeader = " "                              // bare space (no glyph for info lines)
)

// clip truncates s to at most max display columns (rune count, a good
// enough proxy here), appending "…" when it cuts. max <= 0 means "no
// limit" (terminal width unknown). Keeping rendered lines within the
// terminal width is what prevents wrapping, which would otherwise desync
// the cursor-up redraw and flood the screen.
func clip(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func animatedDotColor(frame int) string {
	return breathingColors[frame%len(breathingColors)] + "⏺\033[0m"
}

func stageFailErrorLine(s string) string {
	// Render the error in the same red as the dot, dimmer for body.
	return "\033[38;2;255;107;138m" + s + "\033[0m"
}
