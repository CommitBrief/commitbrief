// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultAPIURL is the only endpoint CommitBrief ever contacts on its
// own behalf, and only when the user runs `commitbrief upgrade`
// (ADR-0034 §D3 — there is no automatic update check). "latest"
// excludes prereleases, so -rc tags are never offered.
const DefaultAPIURL = "https://api.github.com/repos/CommitBrief/commitbrief/releases/latest"

// ReleasesPage is shown to the user when an automated path is not
// available (no asset for their platform, unwritable target).
const ReleasesPage = "https://github.com/CommitBrief/commitbrief/releases"

// maxDownloadBytes caps any single response body. A release archive is
// a few megabytes; the cap only exists so a malformed or hostile
// response cannot fill the disk.
const maxDownloadBytes = 200 << 20 // 200 MiB

var (
	// ErrRateLimited is the unauthenticated GitHub API hourly cap.
	ErrRateLimited = errors.New("github api rate limit exceeded")
	// ErrNoRelease means the repository has no published release.
	ErrNoRelease = errors.New("no published release found")
)

// Asset is one file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release is the subset of the GitHub release payload we use.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// AssetByName finds an attached file by its exact name.
func (r *Release) AssetByName(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Client talks to the GitHub Releases API. APIURL is a field so tests
// can point it at an httptest server — no test ever reaches github.com.
type Client struct {
	HTTP      *http.Client
	APIURL    string
	UserAgent string
}

// NewClient returns a client that identifies itself with the running
// CommitBrief version and gives up after 30 seconds.
func NewClient(version string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		APIURL:    DefaultAPIURL,
		UserAgent: "commitbrief/" + version,
	}
}

// Latest fetches the newest published (non-prerelease) release.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, ErrRateLimited
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoRelease
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("github api: unexpected status %s", resp.Status)
	}

	var rel Release
	// The raw body is deliberately not echoed on a parse failure: an
	// error page can be arbitrarily long and is never actionable.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDownloadBytes)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	if rel.TagName == "" {
		return nil, ErrNoRelease
	}
	return &rel, nil
}

// Download streams url into w. Redirects are followed (GitHub sends
// release downloads to objects.githubusercontent.com).
func (c *Client) Download(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, maxDownloadBytes))
	return err
}
