package builtin

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// archiveExts are container formats CollectFolder unpacks to reach the documents
// inside. GKUOKS Rosreestr exports ship as .zip; .rar/.7z cover ad-hoc archives.
var archiveExts = []string{".zip", ".rar", ".7z"}

// extractArchive unpacks the archive at src into destDir and returns local paths
// of members whose extension is in exts (recursively, case-insensitive). A
// corrupt or unsupported member is skipped rather than failing the whole archive,
// so one bad file inside a batch does not sink the review.
func extractArchive(_ context.Context, src, destDir string, exts []string) ([]string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("extract %q: mkdir: %w", src, err)
	}
	switch strings.ToLower(filepath.Ext(src)) {
	case ".zip":
		return extractZip(src, destDir, exts)
	case ".rar":
		return extractRar(src, destDir, exts)
	case ".7z":
		return extract7z(src, destDir, exts)
	default:
		return nil, fmt.Errorf("extract %q: unsupported archive type", src)
	}
}

func extractZip(src, destDir string, exts []string) ([]string, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("open zip %q: %w", src, err)
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !extAllowed(f.Name, exts) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			slog.Warn("archive member open failed", "archive", src, "member", f.Name, "error", err)
			continue
		}
		lp, err := writeMember(destDir, f.Name, rc)
		rc.Close()
		if err != nil {
			slog.Warn("archive member write failed", "archive", src, "member", f.Name, "error", err)
			continue
		}
		out = append(out, lp)
	}
	return out, nil
}

func extractRar(src, destDir string, exts []string) ([]string, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open rar %q: %w", src, err)
	}
	defer f.Close()
	rr, err := rardecode.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read rar %q: %w", src, err)
	}
	var out []string
	for {
		hdr, err := rr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A decode error mid-stream ends harvesting but keeps what we got.
			slog.Warn("rar read failed", "archive", src, "error", err)
			break
		}
		if hdr.IsDir || !extAllowed(hdr.Name, exts) {
			continue
		}
		lp, err := writeMember(destDir, hdr.Name, rr)
		if err != nil {
			slog.Warn("archive member write failed", "archive", src, "member", hdr.Name, "error", err)
			continue
		}
		out = append(out, lp)
	}
	return out, nil
}

func extract7z(src, destDir string, exts []string) ([]string, error) {
	sr, err := sevenzip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("open 7z %q: %w", src, err)
	}
	defer sr.Close()
	var out []string
	for _, f := range sr.File {
		if f.FileInfo().IsDir() || !extAllowed(f.Name, exts) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			slog.Warn("archive member open failed", "archive", src, "member", f.Name, "error", err)
			continue
		}
		lp, err := writeMember(destDir, f.Name, rc)
		rc.Close()
		if err != nil {
			slog.Warn("archive member write failed", "archive", src, "member", f.Name, "error", err)
			continue
		}
		out = append(out, lp)
	}
	return out, nil
}

// writeMember writes one archive member to destDir under a sanitized relative
// name, guarding against path traversal (zip-slip). Returns the local path.
func writeMember(destDir, name string, r io.Reader) (string, error) {
	rel := sanitizeMemberName(name)
	if rel == "" {
		return "", fmt.Errorf("empty member name %q", name)
	}
	root := filepath.Clean(destDir)
	out := filepath.Join(root, rel)
	if out != root && !strings.HasPrefix(out, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("member %q escapes destination", name)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return out, nil
}

// sanitizeMemberName normalizes an archive member path to a safe relative path:
// backslashes → slashes (rar/7z use them), leading slashes stripped, "."/".."
// components dropped. Returns "" when nothing safe remains.
func sanitizeMemberName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	var clean []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".", "..":
			continue
		default:
			clean = append(clean, p)
		}
	}
	return filepath.Join(clean...)
}

// findLocalDocs walks dir and returns files whose extension is in exts, sorted
// for stable order. Used to pick up an archive's reviewable members from an
// already-extracted cache dir without re-unpacking.
func findLocalDocs(dir string, exts []string) []string {
	var out []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if extAllowed(path, exts) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
