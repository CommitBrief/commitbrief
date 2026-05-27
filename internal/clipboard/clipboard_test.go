package clipboard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestEmitOSC52WrapsPayloadWithEscape(t *testing.T) {
	var buf bytes.Buffer
	const payload = "hello clipboard"
	if err := EmitOSC52(&buf, payload); err != nil {
		t.Fatalf("EmitOSC52: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "\x1b]52;c;") {
		t.Errorf("output should start with OSC 52 introducer; got %q", got)
	}
	if !strings.HasSuffix(got, "\x07") {
		t.Errorf("output should end with BEL terminator; got %q", got)
	}
	// Extract the base64 payload between "\x1b]52;c;" and "\x07" and
	// verify it round-trips back to the input.
	body := strings.TrimSuffix(strings.TrimPrefix(got, "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("base64 body not valid: %v (body=%q)", err, body)
	}
	if string(decoded) != payload {
		t.Errorf("decoded body = %q, want %q", decoded, payload)
	}
}

func TestEmitOSC52EmptyPayloadStillWritesEscape(t *testing.T) {
	// Empty payload is a valid clipboard clear request; we should still
	// emit the wrapper so the terminal sees the intent. (We never call
	// this from production code — guard is in cli/review.go — but the
	// transport itself stays well-defined.)
	var buf bytes.Buffer
	if err := EmitOSC52(&buf, ""); err != nil {
		t.Fatalf("EmitOSC52: %v", err)
	}
	got := buf.String()
	if got != "\x1b]52;c;\x07" {
		t.Errorf("empty payload wrapper = %q, want %q", got, "\x1b]52;c;\x07")
	}
}

func TestMethodLabel(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want string
	}{
		{"both", Result{OSC52: true, Native: true}, "OSC 52 + native"},
		{"osc only", Result{OSC52: true}, "OSC 52"},
		{"native only", Result{Native: true}, "native"},
		{"neither", Result{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.MethodLabel(); got != c.want {
				t.Errorf("MethodLabel = %q, want %q", got, c.want)
			}
		})
	}
}
