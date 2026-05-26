package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type MailRuCloud struct {
	email    string
	password string
	basePath string
	client   *http.Client
}

func NewMailRuCloud(email, password, basePath string) *MailRuCloud {
	if basePath == "" {
		basePath = "/"
	}
	return &MailRuCloud{
		email:    email,
		password: password,
		basePath: basePath,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *MailRuCloud) Name() string { return "cloud_files" }
func (m *MailRuCloud) Description() string {
	return "Read and list files in Mail.ru Cloud storage (construction project documents)"
}
func (m *MailRuCloud) Category() string { return "files" }

func (m *MailRuCloud) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "read", "download", "search", "upload"],
				"description": "list: folder contents. read: text file. download: save to server. search: find by keyword. upload: write file to cloud"
			},
			"path": {
				"type": "string",
				"description": "For list/read/download: path like '/folder/file.pdf'. For search: keyword. For upload: destination path in cloud"
			},
			"content": {
				"type": "string",
				"description": "For upload: file content to write"
			}
		},
		"required": ["action", "path"]
	}`)
}

type mailruParams struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (m *MailRuCloud) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p mailruParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	fullPath := m.basePath + strings.TrimPrefix(p.Path, "/")

	switch p.Action {
	case "list":
		return m.listFolder(ctx, fullPath)
	case "read":
		return m.readFile(ctx, fullPath)
	case "download":
		return m.downloadFile(ctx, fullPath)
	case "search":
		return m.searchFiles(ctx, p.Path)
	case "upload":
		return m.uploadFile(ctx, fullPath, p.Content)
	default:
		return nil, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (m *MailRuCloud) listFolder(ctx context.Context, path string) (json.RawMessage, error) {
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	encoded := encodeWebDAVPath(path)
	reqURL := "https://webdav.cloud.mail.ru" + encoded

	req, err := http.NewRequestWithContext(ctx, "PROPFIND", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(m.email, m.password)
	req.Header.Set("Depth", "1")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("WebDAV error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Parse displaynames from XML
	re := regexp.MustCompile(`<d:displayname>([^<]+)</d:displayname>`)
	matches := re.FindAllStringSubmatch(string(body), -1)

	var items []string
	for i, match := range matches {
		if i == 0 {
			continue // skip the folder itself
		}
		if len(match) > 1 {
			items = append(items, match[1])
		}
	}

	result := map[string]any{
		"path":  path,
		"count": len(items),
		"items": items,
	}

	return json.Marshal(result)
}

func (m *MailRuCloud) readFile(ctx context.Context, path string) (json.RawMessage, error) {
	encoded := encodeWebDAVPath(path)
	reqURL := "https://webdav.cloud.mail.ru" + encoded

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(m.email, m.password)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download error: %d", resp.StatusCode)
	}

	// Limit read to 50KB for text files
	limited := io.LimitReader(resp.Body, 50*1024)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")

	// If binary, just report metadata
	if isBinaryContent(contentType, path) {
		result := map[string]any{
			"path":         path,
			"size":         resp.ContentLength,
			"content_type": contentType,
			"message":      "Binary file. Use 'list' to see folder contents. For images, download and send via Telegram.",
		}
		return json.Marshal(result)
	}

	result := map[string]any{
		"path":         path,
		"size":         len(data),
		"content_type": contentType,
		"content":      string(data),
	}

	return json.Marshal(result)
}

func (m *MailRuCloud) uploadFile(ctx context.Context, path, content string) (json.RawMessage, error) {
	encoded := encodeWebDAVPath(path)
	reqURL := "https://webdav.cloud.mail.ru" + encoded

	req, err := http.NewRequestWithContext(ctx, "PUT", reqURL, strings.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(m.email, m.password)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload error %d: %s", resp.StatusCode, string(body))
	}

	result := map[string]any{
		"path":    path,
		"size":    len(content),
		"status":  "uploaded",
		"message": fmt.Sprintf("File uploaded to cloud: %s", path),
	}
	return json.Marshal(result)
}

func (m *MailRuCloud) searchFiles(ctx context.Context, keyword string) (json.RawMessage, error) {
	keyword = strings.ToLower(keyword)

	// List root to find matching folders
	rootResult, err := m.listFolder(ctx, m.basePath)
	if err != nil {
		return nil, fmt.Errorf("list root: %w", err)
	}

	var rootData struct {
		Items []string `json:"items"`
	}
	json.Unmarshal(rootResult, &rootData)

	var matchedFolders []string
	for _, item := range rootData.Items {
		if strings.Contains(strings.ToLower(item), keyword) {
			matchedFolders = append(matchedFolders, item)
		}
	}

	if len(matchedFolders) == 0 {
		result := map[string]any{
			"keyword": keyword,
			"message": "No folders found matching keyword",
			"all_folders": rootData.Items,
		}
		return json.Marshal(result)
	}

	// List files in each matched folder with exact paths
	type fileEntry struct {
		Name string `json:"name"`
		Path string `json:"path"` // exact path for download
	}
	type folderContent struct {
		Folder     string      `json:"folder"`
		FolderPath string      `json:"folder_path"` // exact path for list
		Files      []fileEntry `json:"files"`
	}

	var results []folderContent
	for _, folder := range matchedFolders {
		folderPath := m.basePath + folder + "/"
		filesResult, err := m.listFolder(ctx, folderPath)
		if err != nil {
			continue
		}
		var filesData struct {
			Items []string `json:"items"`
		}
		json.Unmarshal(filesResult, &filesData)

		var files []fileEntry
		for _, f := range filesData.Items {
			files = append(files, fileEntry{
				Name: f,
				Path: "/" + folder + "/" + f,
			})
		}

		results = append(results, folderContent{
			Folder:     folder,
			FolderPath: "/" + folder + "/",
			Files:      files,
		})
	}

	result := map[string]any{
		"keyword":     keyword,
		"matches":     results,
		"instruction": "Use the exact 'path' values for download or list actions. Do NOT modify folder names.",
	}
	return json.Marshal(result)
}

func (m *MailRuCloud) downloadFile(ctx context.Context, path string) (json.RawMessage, error) {
	encoded := encodeWebDAVPath(path)
	reqURL := "https://webdav.cloud.mail.ru" + encoded

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(m.email, m.password)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download error: %d", resp.StatusCode)
	}

	// Save to local files directory — sanitize filename (remove spaces)
	downloadDir := filepath.Join(filepath.Dir(m.basePath), "files")
	if downloadDir == "/files" || downloadDir == "files" {
		downloadDir = "/opt/assistant/files"
	}
	os.MkdirAll(downloadDir, 0755)

	filename := filepath.Base(path)
	safeFilename := strings.ReplaceAll(filename, " ", "_")
	localPath := filepath.Join(downloadDir, safeFilename)

	out, err := os.Create(localPath)
	if err != nil {
		return nil, fmt.Errorf("create local file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	// Auto-extract text from PDF
	ext := strings.ToLower(filepath.Ext(safeFilename))
	extractedText := ""
	if ext == ".pdf" {
		cmd := exec.CommandContext(ctx, "pdftotext", localPath, "-")
		output, err := cmd.Output()
		if err == nil {
			extractedText = string(output)
			if len(extractedText) > 6000 {
				extractedText = extractedText[:6000] + "\n\n... (truncated)"
			}
		}
	}

	result := map[string]any{
		"path":       path,
		"local_path": localPath,
		"size":       written,
		"filename":   safeFilename,
	}

	if extractedText != "" {
		result["text_content"] = extractedText
		result["message"] = "File downloaded and text extracted. Content is in text_content field."
	} else {
		result["message"] = fmt.Sprintf("File downloaded to %s. Use bash to analyze.", localPath)
	}

	return json.Marshal(result)
}

func encodeWebDAVPath(path string) string {
	parts := strings.Split(path, "/")
	encoded := make([]string, len(parts))
	for i, part := range parts {
		encoded[i] = url.PathEscape(part)
	}
	return strings.Join(encoded, "/")
}

func isBinaryContent(contentType, path string) bool {
	binaryTypes := []string{"image/", "application/pdf", "application/zip", "application/octet-stream",
		"application/vnd.ms-excel", "application/vnd.openxmlformats", "application/msword"}
	for _, t := range binaryTypes {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	binaryExts := []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".zip", ".rar", ".jpg", ".png", ".dwg"}
	lower := strings.ToLower(path)
	for _, ext := range binaryExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
