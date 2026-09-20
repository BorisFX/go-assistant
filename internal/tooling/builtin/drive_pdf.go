package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/app/docgen"
)

// createPDF renders text into a PDF, puts it in Drive and keeps a local copy —
// the copy is what can then be attached to a tender letter. Contractors expect
// a document, not a .txt in the body.
func (d *DriveFiles) createPDF(ctx context.Context, path, name, content string) (json.RawMessage, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("pdf: нет текста документа")
	}
	if name == "" {
		return nil, fmt.Errorf("pdf: не указано имя файла")
	}
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		name += ".pdf"
	}

	data, err := docgen.MarkdownToPDF(ctx, name, content)
	if err != nil {
		return nil, err
	}

	// The local copy lives in the bot's working folder — only from there can a
	// letter take an attachment.
	localDir := filepath.Join(d.filesDir, "pdf")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	localPath := filepath.Join(localDir, sanitizeFileName(name))
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}

	result := map[string]any{
		"name":       name,
		"local_path": localPath,
		"size":       len(data),
		"hint":       "для вложения в письмо используйте local_path",
	}

	folderID, err := d.client.EnsurePath(ctx, path)
	if err != nil {
		// The file is already built: report the Drive problem but hand back
		// the path for the attachment.
		slog.Warn("pdf: не удалось открыть папку на Диске", "path", path, "error", err)
		result["drive_error"] = err.Error()
		return json.Marshal(result)
	}
	info, err := d.client.Upload(ctx, folderID, name, "application/pdf", data)
	if err != nil {
		slog.Warn("pdf: не удалось загрузить на Диск", "path", path, "error", err)
		result["drive_error"] = err.Error()
		return json.Marshal(result)
	}
	result["file_id"] = info.ID
	result["drive_path"] = strings.Trim(path+"/"+name, "/")
	return json.Marshal(result)
}

// UploadReport puts a finished review report into the reviewed folder on Drive,
// or into «Отчёты» when the folder is not a Drive path (the Mail.ru archive).
// Returns the Drive path of the uploaded file.
func (d *DriveFiles) UploadReport(ctx context.Context, folder, name string, data []byte) (string, error) {
	dest := strings.Trim(folder, "/")
	folderID, err := d.client.EnsurePath(ctx, dest)
	if err != nil {
		dest = reportsFolder
		if folderID, err = d.client.EnsurePath(ctx, dest); err != nil {
			return "", fmt.Errorf("папка для отчёта на Диске: %w", err)
		}
	}
	if _, err := d.client.Upload(ctx, folderID, name, "application/pdf", data); err != nil {
		return "", fmt.Errorf("загрузка отчёта на Диск: %w", err)
	}
	return dest + "/" + name, nil
}

// reportsFolder is the root-level fallback for reports of folders that live
// outside Drive.
const reportsFolder = "Отчёты"
