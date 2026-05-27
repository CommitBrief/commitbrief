// SPDX-License-Identifier: GPL-3.0-or-later

package tokens

import "testing"

func TestEstimate(t *testing.T) {
	// Pin the chars/4 rounding contract since six providers + diff +
	// compress all rely on Estimate producing identical results.
	// Drift here means cache keys / cost preflights / context-window
	// gates disagree about how many tokens a given string is worth.
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"single char rounds up to 1", "x", 1},
		{"3 chars rounds up to 1", "xyz", 1},
		{"4 chars stays 1", "abcd", 1},
		{"5 chars rounds up to 2", "abcde", 2},
		{"8 chars stays 2", "abcdefgh", 2},
		{"9 chars rounds up to 3", "abcdefghi", 3},
		{"large input", string(make([]byte, 4000)), 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Estimate(c.in); got != c.want {
				t.Errorf("Estimate(%d-byte input) = %d, want %d", len(c.in), got, c.want)
			}
		})
	}
}
