package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// drivePutTimeout bounds one --drive-put run; a multi-gigabyte folder over a
// slow link still fits.
const drivePutTimeout = 4 * time.Hour

// driveSink is the slice of the Drive adapter the uploader needs, isolated so
// the mirroring logic is testable without Google.
type driveSink interface {
	EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error)
	List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error)
	UploadStream(ctx context.Context, parentID, name, mimeType string, r io.Reader) (gworkspace.FileInfo, error)
}

type putStats struct {
	Uploaded int
	Skipped  int
	Bytes    int64
}

// runDrivePut is the --drive-put entry: mirror a local directory into the
// shared drive and exit. Returns the process exit code.
func runDrivePut(d driveSink, localDir, dest string) int {
	if dest == "" {
		dest = filepath.Base(filepath.Clean(localDir))
	}
	ctx, cancel := context.WithTimeout(context.Background(), drivePutTimeout)
	defer cancel()

	started := time.Now()
	slog.Info("drive-put: start", "dir", localDir, "dest", dest)
	st, err := putDir(ctx, d, localDir, dest)
	if err != nil {
		slog.Error("drive-put: failed", "error", err, "uploaded", st.Uploaded, "skipped", st.Skipped)
		return 1
	}
	slog.Info("drive-put: done",
		"uploaded", st.Uploaded, "skipped", st.Skipped, "mb", st.Bytes>>20,
		"ms", time.Since(started).Milliseconds())
	return 0
}

// putDir mirrors localDir into dest (a "/"-separated path under the drive
// root), creating folders as needed. Files that already exist on Drive with
// the same size are skipped, so a rerun after a dropped connection only sends
// what is missing. Dotfiles are ignored: Finder litter has no place in a
// client's project folder.
func putDir(ctx context.Context, d driveSink, localDir, dest string) (putStats, error) {
	var st putStats
	info, err := os.Stat(localDir)
	if err != nil {
		return st, err
	}
	if !info.IsDir() {
		return st, fmt.Errorf("drive-put: %s is not a directory", localDir)
	}

	parent := ""
	for _, seg := range strings.Split(strings.Trim(dest, "/"), "/") {
		if seg == "" {
			continue
		}
		folder, err := d.EnsureFolder(ctx, parent, seg)
		if err != nil {
			return st, err
		}
		parent = folder.ID
	}
	if parent == "" {
		return st, fmt.Errorf("drive-put: destination path is empty")
	}
	err = putTree(ctx, d, localDir, parent, &st)
	return st, err
}

func putTree(ctx context.Context, d driveSink, dir, parentID string, st *putStats) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	existing, err := d.List(ctx, parentID)
	if err != nil {
		return fmt.Errorf("drive-put: list %s: %w", dir, err)
	}
	onDrive := make(map[string]gworkspace.FileInfo, len(existing))
	for _, f := range existing {
		onDrive[f.Name] = f
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		local := filepath.Join(dir, name)
		if e.IsDir() {
			folder, err := d.EnsureFolder(ctx, parentID, name)
			if err != nil {
				return err
			}
			if err := putTree(ctx, d, local, folder.ID, st); err != nil {
				return err
			}
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		if have, ok := onDrive[name]; ok {
			if have.IsFolder || have.Size != fi.Size() {
				slog.Warn("drive-put: name already on Drive with different size, skipping; delete it there to re-upload",
					"name", name, "local_bytes", fi.Size(), "drive_bytes", have.Size)
			} else {
				slog.Info("drive-put: already uploaded", "name", name, "bytes", fi.Size())
			}
			st.Skipped++
			continue
		}
		if err := putFile(ctx, d, local, name, parentID, fi.Size()); err != nil {
			return err
		}
		st.Uploaded++
		st.Bytes += fi.Size()
	}
	return nil
}

func putFile(ctx context.Context, d driveSink, local, name, parentID string, size int64) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	started := time.Now()
	if _, err := d.UploadStream(ctx, parentID, name, mimeForPath(name), f); err != nil {
		return err
	}
	slog.Info("drive-put: uploaded", "name", name, "mb", size>>20, "ms", time.Since(started).Milliseconds())
	return nil
}

func mimeForPath(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}
