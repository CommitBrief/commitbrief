// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"regexp"
	"strconv"
	"strings"
)

// markerVersion is the only marker format version this build understands.
// A comment carrying any other `v=` value is treated as foreign and never
// touched (ADR-0046 §1).
const markerVersion = "1"

// markerRe matches the per-comment identity marker:
//
//	<!-- commitbrief:fp=<64 hex> v=1 [key=value ...] -->
//
// Group 1 is the fingerprint, group 2 the raw attribute run.
var markerRe = regexp.MustCompile(`<!--[ \t]*commitbrief:fp=([0-9a-f]{64})((?:[ \t]+[^\s>]+)*)[ \t]*-->`)

// replyMarkerRe matches the status-reply markers (`gone` / `back`). It
// cannot be confused with the identity marker: that one starts with `fp=`.
var replyMarkerRe = regexp.MustCompile(`<!--[ \t]*commitbrief:(gone|back)[ \t]+v=1[ \t]*-->`)

// Marker is a parsed identity marker. Attrs holds every attribute token
// other than `v=1`, in document order — unknown keys and values included —
// so a rewrite preserves what it does not understand.
type Marker struct {
	FP    string
	Attrs []string
}

// NewMarker returns a marker with no attributes (a thread CommitBrief
// manages and has not closed).
func NewMarker(fp string) Marker { return Marker{FP: fp} }

// String renders the marker exactly as it is stored in a comment body.
func (m Marker) String() string {
	var b strings.Builder
	b.WriteString("<!-- commitbrief:fp=")
	b.WriteString(m.FP)
	b.WriteString(" v=")
	b.WriteString(markerVersion)
	for _, a := range m.Attrs {
		b.WriteByte(' ')
		b.WriteString(a)
	}
	b.WriteString(" -->")
	return b.String()
}

// attr returns the value of the first attribute named key.
func (m Marker) attr(key string) (string, bool) {
	prefix := key + "="
	for _, a := range m.Attrs {
		if strings.HasPrefix(a, prefix) {
			return a[len(prefix):], true
		}
	}
	return "", false
}

// State is the `state=` value, "" when absent.
func (m Marker) State() string {
	v, _ := m.attr("state")
	return v
}

// Last is the `last=` comment id; 0 when absent or malformed.
func (m Marker) Last() int64 {
	v, ok := m.attr("last")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// WithState returns a copy with `state` (and `last`, when non-zero)
// replaced. An empty state removes both. Every other attribute is kept as
// is, in order. These are the only two keys CommitBrief ever rewrites.
func (m Marker) WithState(state string, last int64) Marker {
	out := Marker{FP: m.FP}
	for _, a := range m.Attrs {
		if strings.HasPrefix(a, "state=") || strings.HasPrefix(a, "last=") {
			continue
		}
		out.Attrs = append(out.Attrs, a)
	}
	if state != "" {
		out.Attrs = append(out.Attrs, "state="+state)
		if last > 0 {
			out.Attrs = append(out.Attrs, "last="+strconv.FormatInt(last, 10))
		}
	}
	return out
}

// locateMarker finds the last valid v=1 identity marker in a comment body
// and returns its byte span. ok is false when there is none (absent,
// malformed, or another version): such a comment is not ours.
func locateMarker(body string) (start, end int, m Marker, ok bool) {
	locs := markerRe.FindAllStringSubmatchIndex(body, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		loc := locs[i]
		fp := body[loc[2]:loc[3]]
		raw := strings.Fields(body[loc[4]:loc[5]])
		attrs := make([]string, 0, len(raw))
		sawV, bad := false, false
		for _, tok := range raw {
			switch {
			case tok == "v="+markerVersion:
				sawV = true
			case strings.HasPrefix(tok, "v="):
				bad = true
			default:
				attrs = append(attrs, tok)
			}
		}
		if bad || !sawV {
			continue
		}
		return loc[0], loc[1], Marker{FP: fp, Attrs: attrs}, true
	}
	return 0, 0, Marker{}, false
}

// SplitMarker separates a comment body into its text and identity marker.
// ok is false when the body carries no valid v=1 marker. text is
// normalized for comparison (CRLF -> LF, trailing whitespace trimmed).
func SplitMarker(body string) (text string, m Marker, ok bool) {
	start, end, m, ok := locateMarker(body)
	if !ok {
		return NormalizeText(body), Marker{}, false
	}
	return NormalizeText(body[:start] + body[end:]), m, true
}

// rewriteMarker replaces the identity marker inside raw with m, leaving
// every other byte of the body untouched. When raw has no marker, m is
// appended on a new line.
func rewriteMarker(raw string, m Marker) string {
	start, end, _, ok := locateMarker(raw)
	if !ok {
		return withMarker(strings.TrimRight(raw, "\n"), m)
	}
	return raw[:start] + m.String() + raw[end:]
}

// NormalizeText prepares comment text for the "is the body the same?"
// comparison: CRLF -> LF, trailing whitespace trimmed per line and overall.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// statusReplyKind reports the kind of status reply a comment body carries:
// "gone", "back" or "".
func statusReplyKind(body string) string {
	m := replyMarkerRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// withMarker appends the marker to rendered comment text on its own line.
func withMarker(text string, m Marker) string {
	return text + "\n" + m.String()
}
