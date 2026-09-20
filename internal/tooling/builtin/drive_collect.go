package builtin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// driveWalkDepth bounds recursion. A shared drive is a user-editable tree and a
// shortcut loop would otherwise walk forever.
const driveWalkDepth = 6

// CollectFolder gathers reviewable documents from a Drive folder, mirroring
// MailRuCloud.CollectFolder so the "разбери папку" intent works the same over
// either storage. Files are cached under filesDir/drive/<path>, archives are
// unpacked, and the file count is guarded before any download.
func (d *DriveFiles) CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error) {
	folderID, err := d.client.ResolvePath(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("collect folder %q: %w", sub, err)
	}

	var found []driveEntry
	if err := d.walk(ctx, folderID, strings.Trim(sub, "/"), exts, driveWalkDepth, &found); err != nil {
		return nil, fmt.Errorf("collect folder %q: %w", sub, err)
	}
	if maxFiles > 0 && len(found) > maxFiles {
		return nil, fmt.Errorf("collect folder %q: %d файлов превышает лимит %d — сузьте папку",
			sub, len(found), maxFiles)
	}

	local := make([]string, 0, len(found))
	for _, e := range found {
		dest := filepath.Join(d.filesDir, "drive", filepath.FromSlash(e.path))
		if err := d.fetchToFile(ctx, e.id, dest); err != nil {
			slog.Warn("drive collect: download failed", "path", e.path, "error", err)
			continue // a missing file does not fail collection
		}
		if !isArchive(e.name) {
			local = append(local, dest)
			continue
		}
		files, err := extractArchive(ctx, dest, dest+".extract", exts)
		if err != nil {
			slog.Warn("drive collect: archive failed", "path", e.path, "error", err)
			continue // a bad archive is skipped, not fatal to the batch
		}
		local = append(local, files...)
	}

	if len(local) == 0 {
		return nil, fmt.Errorf("collect folder %q: подходящих документов не найдено", sub)
	}
	return local, nil
}

type driveEntry struct {
	id   string
	name string
	path string
}

func (d *DriveFiles) walk(ctx context.Context, folderID, prefix string, exts []string, depth int, out *[]driveEntry) error {
	if depth <= 0 {
		return nil
	}
	files, err := d.client.List(ctx, folderID)
	if err != nil {
		return err
	}
	for _, f := range files {
		path := f.Name
		if prefix != "" {
			path = prefix + "/" + f.Name
		}
		if f.IsFolder {
			if err := d.walk(ctx, f.ID, path, exts, depth-1, out); err != nil {
				return err
			}
			continue
		}
		if extAllowed(f.Name, exts) || isArchive(f.Name) {
			*out = append(*out, driveEntry{id: f.ID, name: f.Name, path: path})
		}
	}
	return nil
}

func isArchive(name string) bool { return extAllowed(name, archiveExts) }

// fetchToFile downloads a Drive file into the local cache. An existing non-empty
// file is reused: within a review run the drive is treated as immutable, and a
// batch of scanned tech plans is expensive to pull twice. The body lands in a
// ".part" file first so an interrupted download is never mistaken for a cache hit.
func (d *DriveFiles) fetchToFile(ctx context.Context, fileID, destPath string) error {
	if fi, err := os.Stat(destPath); err == nil && fi.Size() > 0 {
		return nil
	}
	data, err := d.client.Download(ctx, fileID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	part := destPath + ".part"
	if err := os.WriteFile(part, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", part, err)
	}
	return os.Rename(part, destPath)
}

// Ensure the Drive collector keeps the same shape as the Mail.ru one; the
// telegram handler holds both behind one interface.
var _ interface {
	CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error)
} = (*DriveFiles)(nil)

var _ interface {
	CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error)
} = (*MailRuCloud)(nil)
