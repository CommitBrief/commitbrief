// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient("v1.14.0")
	c.APIURL = srv.URL
	return c, srv
}

func TestLatestParsesRelease(t *testing.T) {
	body := `{
		"tag_name": "v1.15.0",
		"html_url": "https://github.com/CommitBrief/commitbrief/releases/tag/v1.15.0",
		"assets": [
			{"name": "commitbrief_1.15.0_linux_x86_64.tar.gz", "browser_download_url": "https://example.test/a.tar.gz"},
			{"name": "checksums.txt", "browser_download_url": "https://example.test/checksums.txt"}
		]
	}`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "commitbrief/v1.14.0" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		_, _ = w.Write([]byte(body))
	}))

	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if rel.TagName != "v1.15.0" {
		t.Fatalf("TagName = %q", rel.TagName)
	}
	a, ok := rel.AssetByName("checksums.txt")
	if !ok || a.BrowserDownloadURL != "https://example.test/checksums.txt" {
		t.Fatalf("AssetByName(checksums.txt) = %+v, ok=%v", a, ok)
	}
	if _, ok := rel.AssetByName("nope"); ok {
		t.Fatal("AssetByName(nope) should not be found")
	}
}

func TestLatestRateLimited(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	_, err := c.Latest(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
}

func TestLatestNoRelease(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.Latest(context.Background())
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("error = %v, want ErrNoRelease", err)
	}
}

// TestLatestMalformedJSON pins that a decode failure is reported as
// ErrBadResponse — the server was reached and answered, so this must
// not collapse into the same message as a network failure.
func TestLatestMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	_, err := c.Latest(context.Background())
	if !errors.Is(err, ErrBadResponse) {
		t.Fatalf("error = %v, want ErrBadResponse", err)
	}
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrNoRelease) {
		t.Fatalf("error = %v, want a plain parse error", err)
	}
}

func TestLatestServerError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("Latest() error = nil, want a status error")
	}
}

func TestDownloadWritesBody(t *testing.T) {
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	var buf bytes.Buffer
	if err := c.Download(context.Background(), srv.URL, &buf); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if buf.String() != "payload" {
		t.Fatalf("body = %q", buf.String())
	}
}

func TestDownloadRejectsNon200(t *testing.T) {
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	var buf bytes.Buffer
	if err := c.Download(context.Background(), srv.URL, &buf); err == nil {
		t.Fatal("Download() error = nil, want a status error")
	}
}

// TestNewClientTransportConfiguration pins the exact fields NewClient
// produces for Assets (the client Download uses) versus HTTP (the API
// client) — a structural check, not a timing-based one, because a
// timing-based test cannot reliably distinguish "no whole-request
// deadline" from "a deadline long enough not to fire in this test run"
// without either being slow or being flaky. A previous version of this
// test used a hand-built client with a short delay and passed
// regardless of what NewClient actually configured (proven by setting
// Assets.Timeout to 1ms and re-running: still green) — this test reads
// the fields straight off NewClient's return value instead, so it fails
// immediately if either regresses:
//   - HTTP keeps a whole-request Timeout (release metadata is small).
//   - Assets has Timeout == 0 (no whole-request deadline on a
//     multi-megabyte download).
//   - Assets' Transport is asserted as *http.Transport with a non-nil
//     Proxy (so HTTPS_PROXY/HTTP_PROXY isn't silently dropped for
//     downloads only) and a non-zero TLSHandshakeTimeout (so a stalled
//     handshake — which ResponseHeaderTimeout does not bound, since it
//     only starts counting after connect+TLS finish — cannot hang
//     forever), confirming Assets was built from a clone of
//     http.DefaultTransport rather than a bare &http.Transport{}.
//   - Assets' Transport.ResponseHeaderTimeout is exactly 30s.
func TestNewClientTransportConfiguration(t *testing.T) {
	c := NewClient("v1.14.0")

	if c.HTTP.Timeout != 10*time.Second {
		t.Fatalf("HTTP.Timeout = %v, want 10s", c.HTTP.Timeout)
	}

	if c.Assets.Timeout != 0 {
		t.Fatalf("Assets.Timeout = %v, want 0 (no whole-request deadline)", c.Assets.Timeout)
	}
	tr, ok := c.Assets.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Assets.Transport = %T, want *http.Transport", c.Assets.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("Assets.Transport.Proxy is nil — HTTPS_PROXY/HTTP_PROXY would be ignored for asset downloads")
	}
	if tr.TLSHandshakeTimeout == 0 {
		t.Fatal("Assets.Transport.TLSHandshakeTimeout is 0 — a stalled TLS handshake would never time out")
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("Assets.Transport.ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
}

// TestDownloadSurvivesSlowBody exercises Download against the actual
// client NewClient produces (not a test-only override): headers arrive
// immediately, then the body trickles in after a short delay. This
// alone cannot prove there is no whole-request deadline — that boundary
// is pinned structurally, at the field level, by
// TestNewClientTransportConfiguration above — but it does catch a
// regression that reintroduces a deadline through some other path (a
// context timeout, a per-request deadline) without necessarily changing
// the Assets.Timeout field that test inspects.
func TestDownloadSurvivesSlowBody(t *testing.T) {
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("payload"))
	}))

	var buf bytes.Buffer
	if err := c.Download(context.Background(), srv.URL, &buf); err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if buf.String() != "payload" {
		t.Fatalf("body = %q, want %q", buf.String(), "payload")
	}
}
