// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrNotWritable means the directory holding the binary cannot be
	// written by this user. Reported before any download happens.
	ErrNotWritable = errors.New("target directory is not writable")
	// ErrAssetMissing means the release has no archive for this platform.
	ErrAssetMissing = errors.New("no release asset for this platform")
	// ErrChecksumMismatch means the downloaded bytes did not match
	// checksums.txt. Nothing is installed.
	ErrChecksumMismatch = errors.New("checksum mismatch")
)

// ManualOptions carries everything InstallManual needs. Target is the
// resolved (symlink-free) path of the running binary.
type ManualOptions struct {
	Client  *Client
	Release *Release
	Target  string
	GOOS    string
	GOARCH  string
}

// PreflightWritable reports whether the binary can be swapped. It
// probes the *directory*, not the file: replacement is a rename, and
// rename permission comes from the parent directory. This is what
// stops the manual path from touching a root-owned /usr/bin or a
// read-only /nix/store — a distro-packaged install that Detect could
// only classify as "manual".
func PreflightWritable(target string) error {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".commitbrief-probe-*")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotWritable, dir)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// InstallManual downloads the release archive for this platform,
// verifies its SHA-256 against checksums.txt, extracts just the binary,
// and swaps it into place.
//
// Trust model: the anchor is TLS to github.com. checksums.txt is not
// signed, so this detects a truncated or corrupted download, not a
// compromised release (ADR-0034 §D7).
func InstallManual(ctx context.Context, o ManualOptions) error {
	assetName := AssetName(o.Release.TagName, o.GOOS, o.GOARCH)
	asset, ok := o.Release.AssetByName(assetName)
	if !ok {
		return fmt.Errorf("%w: %s", ErrAssetMissing, assetName)
	}
	sumsAsset, ok := o.Release.AssetByName(ChecksumsFile)
	if !ok {
		return fmt.Errorf("%w: %s", ErrAssetMissing, ChecksumsFile)
	}

	var sumsBuf bytes.Buffer
	if err := o.Client.Download(ctx, sumsAsset.BrowserDownloadURL, &sumsBuf); err != nil {
		return err
	}
	want := ParseChecksums(sumsBuf.Bytes())[assetName]
	if want == "" {
		return fmt.Errorf("%w: %s has no entry for %s", ErrChecksumMismatch, ChecksumsFile, assetName)
	}

	dir := filepath.Dir(o.Target)

	// The archive lands next to the binary so the later rename stays on
	// one filesystem and therefore stays atomic.
	archiveFile, err := os.CreateTemp(dir, ".commitbrief-dl-*")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotWritable, dir)
	}
	archivePath := archiveFile.Name()
	defer func() {
		_ = archiveFile.Close()
		_ = os.Remove(archivePath)
	}()

	hasher := sha256.New()
	if err := o.Client.Download(ctx, asset.BrowserDownloadURL, io.MultiWriter(archiveFile, hasher)); err != nil {
		return err
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != want {
		return fmt.Errorf("%w: %s (want %s, got %s)", ErrChecksumMismatch, assetName, want, got)
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	binFile, err := os.CreateTemp(dir, ".commitbrief-bin-*")
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotWritable, dir)
	}
	binPath := binFile.Name()
	// Removed unconditionally: on success the rename has already taken
	// the file away, and Remove on a missing path is harmless.
	defer func() {
		_ = binFile.Close()
		_ = os.Remove(binPath)
	}()

	entry := BinaryEntryName(o.GOOS)
	if o.GOOS == "windows" {
		info, statErr := archiveFile.Stat()
		if statErr != nil {
			return statErr
		}
		err = extractZip(archiveFile, info.Size(), entry, binFile)
	} else {
		err = extractTarGz(archiveFile, entry, binFile)
	}
	if err != nil {
		return err
	}
	if err := binFile.Close(); err != nil {
		return err
	}

	// Carry over the existing binary's mode instead of forcing 0755 —
	// a deliberately locked-down install stays locked down.
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(o.Target); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(binPath, mode); err != nil {
		return err
	}

	return replaceBinary(binPath, o.Target)
}

// extractTarGz writes the single entry named want into w.
func extractTarGz(r io.Reader, want string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive has no %q entry", want)
		}
		if err != nil {
			return err
		}
		if err := checkEntryName(hdr.Name); err != nil {
			return err
		}
		if hdr.Name != want {
			continue
		}
		if _, err := io.Copy(w, io.LimitReader(tr, maxDownloadBytes)); err != nil {
			return err
		}
		return nil
	}
}

// extractZip writes the single entry named want into w.
func extractZip(r io.ReaderAt, size int64, want string, w io.Writer) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if err := checkEntryName(f.Name); err != nil {
			return err
		}
		if f.Name != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		if _, err := io.Copy(w, io.LimitReader(rc, maxDownloadBytes)); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("archive has no %q entry", want)
}

// checkEntryName rejects absolute paths and parent-directory escapes.
// Only one known-named entry is ever extracted, so this cannot trigger
// on a well-formed release — it is a standing guard against a malformed
// or hostile archive (tar-slip / zip-slip).
func checkEntryName(name string) error {
	clean := path.Clean(filepath.ToSlash(name))
	if path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("refusing unsafe archive entry %q", name)
	}
	return nil
}
