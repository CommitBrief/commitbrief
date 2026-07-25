// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// buildTarGz returns a gzip'd tar holding one root-level entry per map
// key — the same shape goreleaser produces (binary at the root).
func buildTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// serveRelease starts a server that hands out the archive and its
// checksum manifest, and returns a Release pointing at it.
func serveRelease(t *testing.T, tag string, archive []byte, sum string) (*Client, *Release) {
	t.Helper()
	assetName := AssetName(tag, runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+ChecksumsFile, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", sum, assetName)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rel := &Release{
		TagName: tag,
		HTMLURL: srv.URL,
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
			{Name: ChecksumsFile, BrowserDownloadURL: srv.URL + "/" + ChecksumsFile},
		},
	}
	return NewClient("v1.14.0"), rel
}

func TestInstallManualReplacesBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("archive fixture is tar.gz; the windows asset is a zip (covered by TestExtractZip)")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}

	archive := buildTarGz(t, map[string]string{
		BinaryEntryName(runtime.GOOS): "NEW BINARY",
		"LICENSE":                     "GPL",
	})
	client, rel := serveRelease(t, "v1.15.0", archive, sha256Hex(archive))

	err := InstallManual(context.Background(), ManualOptions{
		Client: client, Release: rel, Target: target,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if err != nil {
		t.Fatalf("InstallManual() error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW BINARY" {
		t.Fatalf("target content = %q, want NEW BINARY", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755 preserved from the old binary", info.Mode().Perm())
	}
	// No .commitbrief-* scratch files may survive a successful run.
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".commitbrief-*"))
	if len(leftovers) != 0 {
		t.Fatalf("scratch files left behind: %v", leftovers)
	}
}

func TestInstallManualRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz fixture")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}

	archive := buildTarGz(t, map[string]string{BinaryEntryName(runtime.GOOS): "NEW BINARY"})
	// Advertise a sum for different bytes.
	client, rel := serveRelease(t, "v1.15.0", archive, sha256Hex([]byte("something else")))

	err := InstallManual(context.Background(), ManualOptions{
		Client: client, Release: rel, Target: target,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD BINARY" {
		t.Fatalf("target was modified despite the mismatch: %q", got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".commitbrief-*"))
	if len(leftovers) != 0 {
		t.Fatalf("scratch files left behind: %v", leftovers)
	}
}

func TestInstallManualMissingAsset(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := &Release{TagName: "v1.15.0", Assets: []Asset{{Name: "unrelated.txt"}}}

	err := InstallManual(context.Background(), ManualOptions{
		Client: NewClient("v1.14.0"), Release: rel, Target: target,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if !errors.Is(err, ErrAssetMissing) {
		t.Fatalf("error = %v, want ErrAssetMissing", err)
	}
}

func TestPreflightWritable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PreflightWritable(target); err != nil {
		t.Fatalf("PreflightWritable() error = %v, want nil", err)
	}
}

func TestPreflightWritableFailsOnReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not model windows ACLs")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "commitbrief")
	if err := os.WriteFile(target, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := PreflightWritable(target); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("error = %v, want ErrNotWritable", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"../escape": "EVIL"})
	var out bytes.Buffer
	err := extractTarGz(bytes.NewReader(archive), "../escape", &out)
	if err == nil {
		t.Fatal("extractTarGz() error = nil, want a rejection")
	}
}

func TestExtractTarGzEntryNotFound(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"LICENSE": "GPL"})
	var out bytes.Buffer
	if err := extractTarGz(bytes.NewReader(archive), "commitbrief", &out); err == nil {
		t.Fatal("extractTarGz() error = nil, want entry-not-found")
	}
}
