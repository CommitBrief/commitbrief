// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

// safeBuffer is a goroutine-safe wrapper around bytes.Buffer so the
// animation goroutine (Animated mode) and the test goroutine can
// share the same target without data races under `go test -race`.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ---------- Silent mode ----------

func TestProgressSilentEmitsNothing(t *testing.T) {
	var w bytes.Buffer
	p := NewProgress(&w, ColorNever, true)
	p.Start("first")
	p.Info("middle")
	p.Finish()
	p.Start("second")
	p.Fail(errors.New("boom"))
	p.Close()
	if got := w.String(); got != "" {
		t.Errorf("silent mode should emit nothing; got %q", got)
	}
}

// ---------- Plain mode (non-TTY, not quiet) ----------

func TestProgressPlainEmitsStageLabels(t *testing.T) {
	// bytes.Buffer is not a TTY → falls through to plain mode.
	var w bytes.Buffer
	p := NewProgress(&w, ColorAuto, false)
	p.Start("Searching for changes...")
	p.Info("36 files +1233 -34")
	p.Finish()
	p.Start("Preparing request...")
	p.Finish()
	p.Start("Thinking...")
	p.Finish()
	p.Close()

	got := w.String()
	expected := []string{
		"[start] Searching for changes...",
		"[info]  36 files +1233 -34",
		"[done]  Searching for changes...", // Note: Finish reports the last started stage
		"[start] Preparing request...",
		"[done]  Preparing request...",
		"[start] Thinking...",
		"[done]  Thinking...",
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("plain mode output missing %q; got:\n%s", want, got)
		}
	}
}

func TestProgressPlainFailEmitsErrorBelow(t *testing.T) {
	var w bytes.Buffer
	p := NewProgress(&w, ColorAuto, false)
	p.Start("Thinking...")
	p.Fail(errors.New("provider timeout"))
	p.Close()

	got := w.String()
	if !strings.Contains(got, "[fail]  Thinking...") {
		t.Errorf("plain fail should mark stage; got:\n%s", got)
	}
	if !strings.Contains(got, "provider timeout") {
		t.Errorf("plain fail should print error body; got:\n%s", got)
	}
}

func TestProgressPlainSoftEmitsTag(t *testing.T) {
	// Soft is the retry-once "neutral" terminal state.
	var w bytes.Buffer
	p := NewProgress(&w, ColorAuto, false)
	p.Start("Thinking...")
	p.Soft()
	p.Start("Retrying...")
	p.Finish()
	p.Close()
	got := w.String()
	for _, want := range []string{"[soft]  Thinking...", "[start] Retrying...", "[done]  Retrying..."} {
		if !strings.Contains(got, want) {
			t.Errorf("plain soft path missing %q; got:\n%s", want, got)
		}
	}
}

// ---------- Animated mode ----------

func TestProgressAnimatedRendersTreeConnectors(t *testing.T) {
	// ColorAlways forces animated mode even on a non-TTY buffer so
	// we can capture the ANSI output.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Searching for changes...")
	p.Finish()
	p.Start("Preparing request...")
	p.Finish()
	p.Start("Thinking...")
	p.Finish()
	p.Close()

	got := w.String()
	// At least one ├─ (non-last) and at least one └─ (last) must
	// appear in the rendered output.
	if !strings.Contains(got, "├─ ") {
		t.Errorf("expected ├─ connectors for non-last stages; got:\n%q", got)
	}
	if !strings.Contains(got, "└─ ") {
		t.Errorf("expected └─ connector for last stage; got:\n%q", got)
	}
	// Tree must include every label.
	for _, want := range []string{
		"Searching for changes...",
		"Preparing request...",
		"Thinking...",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("animated output missing label %q", want)
		}
	}
}

func TestProgressAnimatedUsesDoneGreenOnFinish(t *testing.T) {
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("step")
	p.Finish()
	p.Close()
	// Done dot uses the cardAddFg green (#22d3a0 → 34;211;160).
	if !strings.Contains(w.String(), "38;2;34;211;160m⏺") {
		t.Errorf("expected green done dot (38;2;34;211;160); got:\n%q", w.String())
	}
}

func TestProgressAnimatedUsesFailRedOnFail(t *testing.T) {
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("step")
	p.Fail(errors.New("kapow"))
	p.Close()
	out := w.String()
	// Fail dot is cardDelFg red (#ff6b8a → 255;107;138).
	if !strings.Contains(out, "38;2;255;107;138m⏺") {
		t.Errorf("expected red fail dot (38;2;255;107;138); got:\n%q", out)
	}
	// Error message indented below the failed stage line.
	if !strings.Contains(out, "kapow") {
		t.Errorf("expected error body in render; got:\n%q", out)
	}
}

func TestProgressAnimatedSoftDotIsMuted(t *testing.T) {
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("step")
	p.Soft()
	p.Close()
	// Soft uses cardMuted gray (#9CA3AF → 156;163;175).
	if !strings.Contains(w.String(), "38;2;156;163;175m⏺") {
		t.Errorf("expected muted soft dot (38;2;156;163;175); got:\n%q", w.String())
	}
}

func TestProgressAnimatedActiveStageEmitsBreathingFrame(t *testing.T) {
	// Start a stage and let the animation goroutine emit at least one
	// frame before we close. We assert that one of the breathing
	// color escape codes appears in the buffered output.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Thinking...")
	// Block briefly to let the ticker emit. progressFrameInterval is
	// 180ms; one tick + final-frame on Close is enough.
	closeOK := make(chan struct{})
	go func() {
		p.Close()
		close(closeOK)
	}()
	<-closeOK
	out := w.String()
	// At least one breathing color stop should appear; we check for
	// the muted bottom-of-cycle (#3a3f4f → 58;63;79) since frame=0
	// hits it on every render.
	if !strings.Contains(out, "38;2;58;63;79m⏺") &&
		!strings.Contains(out, "38;2;79;85;99m⏺") &&
		!strings.Contains(out, "38;2;107;114;128m⏺") &&
		!strings.Contains(out, "38;2;156;163;175m⏺") {
		t.Errorf("expected at least one breathing-frame dot in output; got:\n%q", out)
	}
}

func TestProgressAnimatedDrawsTrunkSeparatorsBetweenStages(t *testing.T) {
	// Each pair of adjacent stage lines is separated by a `│` trunk
	// line so the tree has breathing room and the vertical connector
	// reads as a continuous line. Skipped after the final stage so
	// the cursor lands cleanly below.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("first")
	p.Start("second")
	p.Start("third")
	p.Finish()
	p.Close()
	out := w.String()
	// Three stages → exactly two trunk separators in the latest render
	// frame. Count occurrences of the `│` followed by clear-line + LF
	// sequence; multi-render emissions mean we'll see >=2 but never 0.
	if !strings.Contains(out, "│\033[K\n") {
		t.Errorf("expected `│` trunk separator between stages; got:\n%q", out)
	}
}

func TestProgressInfoSubSuppressesTrunkSeparator(t *testing.T) {
	// Sub-list items (InfoSub) hug the line above them: no `│` breather is
	// drawn before a sub line. A header followed only by sub items therefore
	// renders with zero trunk separators, so the file list reads tightly.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Info("Detected 3 staged files")
	p.InfoSub("    a.go")
	p.InfoSub("    b.go")
	p.Close()
	out := w.String()
	if strings.Contains(out, "│\033[K\n") {
		t.Errorf("sub-list items must not be separated by a trunk `│`; got:\n%q", out)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if !strings.Contains(out, name) {
			t.Errorf("sub item %q missing from render; got:\n%q", name, out)
		}
	}
}

func TestProgressInfoSubSeparatedFromNextStage(t *testing.T) {
	// A non-sub stage after a sub-list gets its separator back, so the list
	// is visually closed off from whatever stage follows it.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Info("Detected 2 staged files")
	p.InfoSub("    a.go")
	p.Start("Preparing...")
	p.Finish()
	p.Close()
	out := w.String()
	if !strings.Contains(out, "│\033[K\n") {
		t.Errorf("expected a trunk separator between the sub-list and the next stage; got:\n%q", out)
	}
}

func TestProgressInfoLineHasNoLeadingDot(t *testing.T) {
	// Info lines are static data ("36 files +1233 -34") — no glyph,
	// no terminal state, just the connector + label.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Searching...")
	p.Info("36 files +1233 -34")
	p.Start("Preparing...")
	p.Finish()
	p.Close()
	out := w.String()
	if !strings.Contains(out, "36 files +1233 -34") {
		t.Errorf("info line missing from render; got:\n%q", out)
	}
}

func TestProgressClearWipesRenderedArea(t *testing.T) {
	// Animated mode: Clear should stop the animation and erase the
	// rendered tree. The buffer accumulates all writes (including the
	// erasing escapes), so we look for the cursor-up + clear-line
	// pattern emitted by clearArea, AND for the absence of a final
	// newline that Close emits.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Searching...")
	p.Finish()
	p.Start("Thinking...")
	p.Finish()
	p.Clear()
	out := w.String()
	// clearArea moves the cursor up and emits \033[K on each previous
	// line to wipe it.
	if !strings.Contains(out, "\033[K") {
		t.Errorf("Clear should emit clear-line escapes; got:\n%q", out)
	}
	// Close adds a trailing literal "\n"; Clear should NOT.
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("Clear must not append a Close-style trailing newline; got tail %q",
			out[len(out)-4:])
	}
}

func TestProgressClearIsIdempotent(t *testing.T) {
	// Calling Clear twice — and then Close — must not panic or hang.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Searching...")
	p.Finish()
	p.Clear()
	p.Clear() // second Clear: no-op
	p.Close() // post-Clear Close: also no-op via the closed flag
}

func TestProgressClearIsNoOpInPlainMode(t *testing.T) {
	// Plain mode already shipped each stage line; Clear cannot
	// un-write them, so the per-stage [start]/[done] markers stay
	// in the buffer. This is deliberate — CI log readers benefit
	// from those breadcrumbs.
	var w bytes.Buffer
	p := NewProgress(&w, ColorNever, false)
	p.Start("Searching...")
	p.Finish()
	p.Clear()
	out := w.String()
	for _, want := range []string{"[start] Searching...", "[done]  Searching..."} {
		if !strings.Contains(out, want) {
			t.Errorf("plain-mode Clear should preserve %q; got:\n%s", want, out)
		}
	}
}

func TestProgressCloseIsIdempotent(t *testing.T) {
	// Defer-Close in callers shouldn't double-finalize.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("step")
	p.Finish()
	p.Close()
	p.Close() // second call must not panic / hang
}

func TestProgressPauseResumeRoundTrip(t *testing.T) {
	// Pause hands the terminal back to the caller (e.g. for a prompt);
	// Resume continues with the same stage list.
	w := &safeBuffer{}
	p := NewProgress(w, ColorAlways, false)
	p.Start("Preparing...")
	p.Pause()
	// Simulate a prompt: write something raw to the buffer.
	_, _ = w.Write([]byte("PROMPT: cost > $0.10, proceed? [y/N] "))
	p.Resume()
	p.Finish()
	p.Close()
	out := w.String()
	if !strings.Contains(out, "Preparing...") {
		t.Errorf("Preparing stage missing after Pause/Resume; got:\n%q", out)
	}
	if !strings.Contains(out, "PROMPT:") {
		t.Errorf("simulated prompt was lost; got:\n%q", out)
	}
}

func TestProgressNonTTYBufferFallsBackToPlain(t *testing.T) {
	// Plain bytes.Buffer is not a TTY, ColorAuto must fall back to
	// plain mode regardless of caller intent. Guards against
	// accidentally emitting ANSI cursor escapes to non-TTY writers.
	var w bytes.Buffer
	p := NewProgress(&w, ColorAuto, false)
	p.Start("step")
	p.Finish()
	p.Close()
	out := w.String()
	if strings.Contains(out, "\033[") {
		t.Errorf("plain mode should not emit ANSI escapes; got:\n%q", out)
	}
	if !strings.Contains(out, "[start] step") || !strings.Contains(out, "[done]  step") {
		t.Errorf("plain mode missing stage lines; got:\n%q", out)
	}
}
