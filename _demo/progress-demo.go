// SPDX-License-Identifier: GPL-3.0-or-later

// Progress UI playground.
//
// Run from the commitbrief/ directory:
//
//	go run _demo/progress-demo.go              # happy path (default)
//	go run _demo/progress-demo.go happy        # 4-stage success
//	go run _demo/progress-demo.go retry        # first thinking attempt fails, retry recovers
//	go run _demo/progress-demo.go fail         # thinking errors out
//	go run _demo/progress-demo.go cache-hit    # search → prepared → done (no thinking stage)
//	go run _demo/progress-demo.go fast         # same as happy but with shorter sleeps
//	go run _demo/progress-demo.go clear        # full happy path → Clear() → simulated cards
//	go run _demo/progress-demo.go close        # full happy path → Close() (tree stays visible)
//
// The `_demo/` directory is automatically excluded from `go build ./...`
// and `go vet ./...` by Go's package matcher (underscore prefix). The
// playground is dev-only — feel free to edit timings, labels, or
// scenarios while iterating on the Progress visual design.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/CommitBrief/commitbrief/internal/ui"
)

func main() {
	scenario := "happy"
	if len(os.Args) > 1 {
		scenario = os.Args[1]
	}

	// Force animated mode regardless of TTY detection so the demo
	// renders the full breathing-dot experience even when stderr is
	// piped (e.g. `go run ... 2>&1 | less -R`).
	p := ui.NewProgress(os.Stderr, ui.ColorAlways, false)
	defer p.Close()

	switch scenario {
	case "happy":
		runHappy(p, 1200, 200, 800, 4500)
	case "fast":
		runHappy(p, 300, 50, 200, 1000)
	case "retry":
		runRetry(p)
	case "fail":
		runFail(p)
	case "cache-hit":
		runCacheHit(p)
	case "clear":
		runClearAfter(p)
	case "close":
		runCloseAfter(p)
	default:
		fmt.Fprintln(os.Stderr, "unknown scenario:", scenario)
		fmt.Fprintln(os.Stderr, "usage: go run _demo/progress-demo.go [happy|fast|retry|fail|cache-hit]")
		os.Exit(2)
	}
}

func runHappy(p *ui.Progress, searchMs, infoMs, prepMs, thinkMs int) {
	p.Start("Searching for changes...")
	time.Sleep(time.Duration(searchMs) * time.Millisecond)
	p.Info("36 files +1233 -34")
	time.Sleep(time.Duration(infoMs) * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(time.Duration(prepMs) * time.Millisecond)
	p.Start("Thinking...")
	time.Sleep(time.Duration(thinkMs) * time.Millisecond)
	p.Finish()
}

func runRetry(p *ui.Progress) {
	p.Start("Searching for changes...")
	time.Sleep(900 * time.Millisecond)
	p.Info("18 files +402 -57")
	time.Sleep(150 * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(600 * time.Millisecond)
	p.Start("Thinking...")
	// First attempt — model returns malformed JSON.
	time.Sleep(3500 * time.Millisecond)
	p.Soft()
	p.Start("Retrying...")
	time.Sleep(2800 * time.Millisecond)
	p.Finish()
}

func runFail(p *ui.Progress) {
	p.Start("Searching for changes...")
	time.Sleep(800 * time.Millisecond)
	p.Info("4 files +12 -3")
	time.Sleep(150 * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(500 * time.Millisecond)
	p.Start("Thinking...")
	time.Sleep(2200 * time.Millisecond)
	p.Fail(errors.New("provider anthropic: 401 unauthorized (check your API key with `commitbrief providers test anthropic`)"))
}

// runClearAfter walks the happy path then calls Clear() — simulates
// the production behavior when the cards renderer is about to take
// over the screen. The progress tree disappears; we then print a
// pretend-card line so the visual transition is obvious.
func runClearAfter(p *ui.Progress) {
	p.Start("Searching for changes...")
	time.Sleep(900 * time.Millisecond)
	p.Info("36 files +1233 -34")
	time.Sleep(150 * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(600 * time.Millisecond)
	p.Start("Thinking...")
	time.Sleep(3500 * time.Millisecond)
	p.Finish()
	p.Clear()
	// Where the real review pipeline would call renderResult():
	fmt.Println("╭───────────────────────── (simulated card render) ─────────────────────────╮")
	fmt.Println("│  💥 CRITICAL  · internal/auth/session.go:142-145                            │")
	fmt.Println("│  SQL fragment built from request input                                      │")
	fmt.Println("╰─────────────────────────────────────────────────────────────────────────────╯")
}

// runCloseAfter walks the same happy path but calls Close() — the
// "no cards rendered" production behavior (no_changes / fail). The
// tree stays visible.
func runCloseAfter(p *ui.Progress) {
	p.Start("Searching for changes...")
	time.Sleep(900 * time.Millisecond)
	p.Info("36 files +1233 -34")
	time.Sleep(150 * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(600 * time.Millisecond)
	p.Start("Thinking...")
	time.Sleep(3500 * time.Millisecond)
	p.Finish()
	p.Close()
	fmt.Println("(no cards rendered; the tree above is the user's only feedback)")
}

func runCacheHit(p *ui.Progress) {
	p.Start("Searching for changes...")
	time.Sleep(700 * time.Millisecond)
	p.Info("9 files +88 -22")
	time.Sleep(150 * time.Millisecond)
	p.Start("Preparing request...")
	time.Sleep(450 * time.Millisecond)
	// Cache hit — no Thinking stage, just finalize Preparing.
	p.Finish()
}
