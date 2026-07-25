// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestLatestMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	_, err := c.Latest(context.Background())
	if err == nil {
		t.Fatal("Latest() error = nil, want a parse error")
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
