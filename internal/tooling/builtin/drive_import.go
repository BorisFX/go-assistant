package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// cloudCollector is the archive storage a project is migrated from. Declared
// here so drive_files does not depend on the Mail.ru tool as a type.
type cloudCollector interface {
	CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error)
	CollectPublic(ctx context.Context, link string, exts []string, maxFiles int) ([]string, error)
	PublicFolderName(ctx context.Context, token string) (string, error)
}

// SetCloudSource enables the import action. Instances without Mail.ru simply
// never get a source, and the action reports that instead of failing obscurely.
func (d *DriveFiles) SetCloudSource(c cloudCollector) { d.cloud = c }

// SetNotify wires the Telegram sink. A migration outlives the chat request that
// started it, so the result is reported separately.
func (d *DriveFiles) SetNotify(fn func(string)) { d.notify = fn }

// importDeadline bounds one migration. Half a gigabyte of photos over two APIs
// takes minutes; the five-minute chat deadline that the request came with does
// not survive it, which is why the work runs detached.
const importDeadline = 45 * time.Minute

// importExtensions is wider than the review set: a migration carries whatever
// the object folder holds, including photos and drawings.
var importExtensions = []string{
	".pdf", ".doc", ".docx", ".xls", ".xlsx", ".xml", ".txt", ".rtf", ".odt",
	".jpg", ".jpeg", ".png", ".tif", ".tiff", ".heic", ".dwg", ".dxf", ".sig",
}

// maxImportFiles guards one migration. A whole multi-object folder is thousands
// of files and would run for hours against both APIs.
const maxImportFiles = 300

// importCloud copies an object's folder from Mail.ru Cloud into Drive, keeping
// the subfolder structure. The bytes never pass through the model: the files are
// fetched to the bot's disk and uploaded from there.
func (d *DriveFiles) importCloud(ctx context.Context, cloudPath, target string) (json.RawMessage, error) {
	if d.cloud == nil {
		return nil, fmt.Errorf("импорт: облако Mail.ru не подключено для этого инстанса")
	}
	if strings.TrimSpace(cloudPath) == "" {
		return nil, fmt.Errorf("импорт: не указана папка в облаке или публичная ссылка")
	}

	token, isPublic := ParsePublicLink(cloudPath)
	if strings.TrimSpace(target) == "" {
		if !isPublic {
			return nil, fmt.Errorf("импорт: не указана папка назначения на Диске")
		}
		// A share link carries the object's name; that is a better folder name
		// than anything the model would invent from the token.
		name, err := d.cloud.PublicFolderName(ctx, token)
		if err != nil {
			return nil, err
		}
		target = name
	}

	// Detached on purpose: the caller's context dies with the chat request, and
	// a folder of scans takes longer than that.
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), importDeadline)
		defer cancel()
		summary, err := d.runImport(bg, cloudPath, target, isPublic)
		if d.notify == nil {
			return
		}
		if err != nil {
			d.notify(fmt.Sprintf("❌ Перенос «%s» не удался: %v", cloudPath, err))
			return
		}
		d.notify(summary)
	}()

	return json.Marshal(map[string]any{
		"from":   cloudPath,
		"to":     target,
		"status": "перенос запущен в фоне; отчёт придёт отдельным сообщением, ждать не нужно",
	})
}

// runImport does the actual copying and returns a human-readable summary.
func (d *DriveFiles) runImport(ctx context.Context, cloudPath, target string, isPublic bool) (string, error) {
	var (
		local []string
		err   error
	)
	if isPublic {
		local, err = d.cloud.CollectPublic(ctx, cloudPath, importExtensions, maxImportFiles)
	} else {
		local, err = d.cloud.CollectFolder(ctx, cloudPath, importExtensions, maxImportFiles)
	}
	if err != nil {
		return "", err
	}

	// The destination is created here, top level included: an import is an
	// explicit instruction to start this project's folder, unlike a write that
	// merely assumes one exists.
	rootID, err := d.ensureTarget(ctx, target)
	if err != nil {
		return "", err
	}

	type failure struct {
		File   string
		Reason string
	}
	var (
		uploaded []string
		skipped  []string
		failures []failure
		folders  = map[string]string{"": rootID}
	)

	for _, path := range local {
		rel := relativeInCache(d.filesDir, path)
		dir, name := filepath.Split(rel)
		dir = filepath.ToSlash(strings.Trim(dir, string(filepath.Separator)))

		folderID, err := d.ensureSubfolder(ctx, folders, target, dir)
		if err != nil {
			failures = append(failures, failure{File: rel, Reason: err.Error()})
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, failure{File: rel, Reason: err.Error()})
			continue
		}
		existing, err := d.client.List(ctx, folderID)
		if err != nil {
			failures = append(failures, failure{File: rel, Reason: err.Error()})
			continue
		}
		if sameFilePresent(existing, name, int64(len(data))) {
			// Re-running a migration must not double every document.
			skipped = append(skipped, rel)
			continue
		}
		if _, err := d.client.Upload(ctx, folderID, name, mimeForName(name), data); err != nil {
			failures = append(failures, failure{File: rel, Reason: err.Error()})
			continue
		}
		uploaded = append(uploaded, rel)
	}

	slog.Info("cloud import finished", "cloud", cloudPath, "target", target,
		"uploaded", len(uploaded), "skipped", len(skipped), "failed", len(failures))

	summary := fmt.Sprintf("📁 Перенос закончен: %s → %s\nЗагружено: %d", cloudPath, target, len(uploaded))
	if len(skipped) > 0 {
		summary += fmt.Sprintf("\nУже было на месте: %d", len(skipped))
	}
	if len(failures) > 0 {
		summary += fmt.Sprintf("\nНе удалось: %d", len(failures))
		for i, f := range failures {
			if i == 3 {
				break
			}
			summary += fmt.Sprintf("\n  %s — %s", f.File, f.Reason)
		}
	}
	return summary, nil
}

// ensureTarget creates the destination path, including its first segment, which
// EnsurePath refuses to do on ordinary writes.
func (d *DriveFiles) ensureTarget(ctx context.Context, target string) (string, error) {
	parts := strings.Split(strings.Trim(target, "/"), "/")
	first, err := d.client.EnsureFolder(ctx, "", parts[0])
	if err != nil {
		return "", fmt.Errorf("создать папку %q: %w", parts[0], err)
	}
	current := first.ID
	for _, part := range parts[1:] {
		next, err := d.client.EnsureFolder(ctx, current, part)
		if err != nil {
			return "", fmt.Errorf("создать папку %q: %w", part, err)
		}
		current = next.ID
	}
	return current, nil
}

// ensureSubfolder mirrors one subfolder of the cloud tree, caching ids so a
// folder with fifty documents is resolved once.
func (d *DriveFiles) ensureSubfolder(ctx context.Context, cache map[string]string, target, dir string) (string, error) {
	if id, ok := cache[dir]; ok {
		return id, nil
	}
	id, err := d.ensureTarget(ctx, target+"/"+dir)
	if err != nil {
		return "", err
	}
	cache[dir] = id
	return id, nil
}

// relativeInCache turns a local cache path back into the path it had in the
// cloud, so the structure is reproduced instead of flattened.
func relativeInCache(filesDir, path string) string {
	for _, root := range []string{filepath.Join(filesDir, "cloud"), filepath.Join(filesDir, "drive"), filesDir} {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(path)
}

func sameFilePresent(existing []gworkspace.FileInfo, name string, size int64) bool {
	for _, f := range existing {
		if f.Name == name && (f.Size == size || f.Size == 0) {
			return true
		}
	}
	return false
}
