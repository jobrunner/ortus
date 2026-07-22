package raster

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/jobrunner/ortus/internal/ports/output"
)

// persistentTempPrefix is the prefix of in-progress extraction dirs in PERSISTENT
// mode; they are renamed onto <id>@<fingerprint> on success. A leftover one is a
// crashed/partial extraction that CleanupOrphaned reclaims.
const persistentTempPrefix = ".ortus-raster-tmp-"

// completeMarker is written (last) into an extraction dir before it is renamed to
// its final <id>@<fingerprint> name. Its presence is what makes a cached dir safe
// to reuse — a partial extraction never has it.
const completeMarker = ".ortus-complete"

// openEphemeral unpacks the bundle into a fresh private temp dir (the default,
// pre-cache behavior): every Open re-extracts and Close removes the dir.
func (r *Repository) openEphemeral(sourceID, path string) (*bundle, error) {
	dir, err := os.MkdirTemp(r.cacheDir, tempDirPrefix+sourceID+"-")
	if err != nil {
		return nil, fmt.Errorf("creating unpack dir: %w", err)
	}
	if err := r.extractTo(path, dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	b, err := r.loadBundle(dir, sourceID, path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return b, nil
}

// openPersistent reuses a content-addressed extraction under cacheRoot when the
// bundle's fingerprint matches; otherwise it extracts once, atomically, and keeps
// it across restarts. The dir is <sourceID>@<fingerprint>, marked complete only
// after a full extract — so a present, complete dir is always safe to reuse.
func (r *Repository) openPersistent(sourceID, path string, span output.Span) (*bundle, error) {
	fp, err := zipFingerprint(path)
	if err != nil {
		return nil, fmt.Errorf("fingerprinting bundle: %w", err)
	}
	target := filepath.Join(r.cacheRoot(), sourceID+"@"+fp)

	if isComplete(target) {
		span.AddEvent("cache_reuse")
		r.logger.Info("reusing cached raster extraction", "id", sourceID, "fingerprint", fp, "dir", target)
		return r.loadBundle(target, sourceID, path)
	}

	span.AddEvent("cache_extract")
	r.logger.Info("extracting raster bundle — no cached extraction for this content", "id", sourceID, "fingerprint", fp)
	tmp, err := os.MkdirTemp(r.cacheRoot(), persistentTempPrefix+sourceID+"-")
	if err != nil {
		return nil, fmt.Errorf("creating extraction temp dir: %w", err)
	}
	if err := r.extractTo(path, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := writeCompleteMarker(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		// A concurrent extractor (e.g. overlapping rolling update on a shared volume)
		// may have created the target first — reuse it rather than fail.
		if isComplete(target) {
			span.AddEvent("cache_reuse_after_race")
			r.logger.Info("reusing concurrently-created raster extraction", "id", sourceID, "fingerprint", fp, "dir", target)
			return r.loadBundle(target, sourceID, path)
		}
		return nil, fmt.Errorf("promoting extraction to %q: %w", target, err)
	}
	if r.prune {
		r.pruneOldVersions(sourceID, fp)
	}
	return r.loadBundle(target, sourceID, path)
}

// extractTo unzips the bundle into dir (decompression-bomb-capped).
func (r *Repository) extractTo(path, dir string) error {
	if err := unzip(path, dir, r.bundleCap()); err != nil {
		return fmt.Errorf("unpacking bundle: %w", err)
	}
	return nil
}

// zipFingerprint returns a short content key derived from the ZIP's central
// directory ONLY — no decompression — so it is cheap even for a 48 GB archive.
// It hashes the sorted per-entry (name, CRC32, uncompressed size) tuples, so any
// added / removed / changed tile flips the key while entry order does not.
func zipFingerprint(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = zr.Close() }()

	lines := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		lines = append(lines, fmt.Sprintf("%s|%d|%d", f.Name, f.CRC32, f.UncompressedSize64))
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		_, _ = io.WriteString(h, l)
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// isComplete reports whether dir is a fully-extracted cache dir (its completeMarker
// exists). A partial extraction — dir absent, or present without the marker — is
// never reused.
func isComplete(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, completeMarker))
	return err == nil
}

// writeCompleteMarker drops the completeMarker into dir. Called as the LAST step
// before the atomic rename, so the marker's presence certifies a full extraction.
func writeCompleteMarker(dir string) error {
	return os.WriteFile(filepath.Join(dir, completeMarker), []byte("ok\n"), 0o600)
}

// pruneOldVersions removes cached extractions of sourceID whose fingerprint is not
// activeFp. UNSAFE during overlapping rolling updates (an old container still opens
// tiles from its dir) — gated behind SetPrune(true).
func (r *Repository) pruneOldVersions(sourceID, activeFp string) {
	matches, err := filepath.Glob(filepath.Join(r.cacheRoot(), sourceID+"@*"))
	if err != nil {
		return
	}
	keep := sourceID + "@" + activeFp
	for _, m := range matches {
		if filepath.Base(m) == filepath.Base(keep) {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			r.logger.Warn("failed to prune stale raster extraction", "dir", m, "error", err)
			continue
		}
		r.logger.Info("pruned stale raster extraction", "dir", m)
	}
}
