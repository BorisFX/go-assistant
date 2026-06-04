package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tree-index limits. The cloud is walked once and cached; search runs locally.
const (
	indexMaxEntries = 50000
	indexMaxDepth   = 12
	indexTTL        = 6 * time.Hour
)

// indexEntry is one node in the cached cloud tree. Path is relative to basePath
// and always starts with "/"; directories also end with "/".
type indexEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type MailRuCloud struct {
	email    string
	password string
	basePath string
	filesDir string // local dir for downloads and the cached index
	client   *http.Client

	indexMu   sync.Mutex
	index     []indexEntry
	indexTime time.Time
}

func NewMailRuCloud(email, password, basePath, filesDir string) *MailRuCloud {
	if basePath == "" {
		basePath = "/"
	}
	if filesDir == "" {
		filesDir = "/opt/assistant/files"
	}
	return &MailRuCloud{
		email:    email,
		password: password,
		basePath: basePath,
		filesDir: filesDir,
		client:   &http.Client{Timeout: 60 * time.Second},
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
				"enum": ["list", "read", "download", "search", "upload", "reindex"],
				"description": "search: find files/folders anywhere by keywords (whole-tree, case-insensitive, word order irrelevant). list: one folder. read: text file. download: save file to server. upload: write file to cloud. reindex: rebuild the local search index"
			},
			"path": {
				"type": "string",
				"description": "For search: keywords describing the file/folder (e.g. 'рускон рв техплан'). For list/read/download: exact path like '/folder/sub/file.pdf'. For upload: destination path in cloud"
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
	case "reindex":
		return m.reindex(ctx)
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
	entries, builtAt, err := m.ensureIndex(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("build cloud index: %w", err)
	}

	tokens := tokenize(keyword)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty search query")
	}

	matches := searchIndex(entries, tokens)

	const maxResults = 40
	truncated := false
	if len(matches) > maxResults {
		matches = matches[:maxResults]
		truncated = true
	}

	result := map[string]any{
		"query":         keyword,
		"index_built":   builtAt.Format("2006-01-02 15:04"),
		"indexed_nodes": len(entries),
		"count":         len(matches),
		"matches":       matches,
		"instruction":   "Use the exact 'path' value for download/list/read. Do NOT alter or guess folder names. If the file you expect is missing, call action 'reindex' (the index may be stale) then search again.",
	}
	if truncated {
		result["note"] = "Results truncated; refine the query with more specific words."
	}
	if len(matches) == 0 {
		result["message"] = "Nothing matched. Try fewer or different words, or 'reindex' if the file is newly added."
	}
	return json.Marshal(result)
}

// WarmIndex builds the cloud tree index if no fresh copy exists yet. Call it in a
// background goroutine at startup so the first user search reads a ready cache
// instead of blocking on a multi-minute WebDAV walk.
func (m *MailRuCloud) WarmIndex(ctx context.Context) {
	if _, _, err := m.ensureIndex(ctx, false); err != nil {
		slog.Warn("mailru index warm failed", "error", err)
		return
	}
	m.indexMu.Lock()
	n := len(m.index)
	m.indexMu.Unlock()
	slog.Info("mailru cloud index ready", "nodes", n)
}

func (m *MailRuCloud) reindex(ctx context.Context) (json.RawMessage, error) {
	entries, builtAt, err := m.ensureIndex(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("reindex: %w", err)
	}
	return json.Marshal(map[string]any{
		"status":        "reindexed",
		"indexed_nodes": len(entries),
		"index_built":   builtAt.Format("2006-01-02 15:04"),
	})
}

type searchMatch struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	score int
}

// searchIndex ranks entries by how many query tokens they contain. Matching is
// OR-based (an entry needs at least minScore tokens) so a query with extra words
// the user misremembered still surfaces the right file. Tokens hitting the leaf
// name and deeper full-token matches score higher, pushing the best hit to the top.
func searchIndex(entries []indexEntry, tokens []string) []searchMatch {
	minScore := 1
	if len(tokens) >= 2 {
		minScore = 2
	}

	var matches []searchMatch
	for _, e := range entries {
		lowerPath := strings.ToLower(e.Path)
		leaf := strings.ToLower(pathLeaf(e.Path))
		score := 0
		for _, t := range tokens {
			if strings.Contains(lowerPath, t) {
				score++
				if strings.Contains(leaf, t) {
					score++ // weight matches in the file/folder name itself
				}
			}
		}
		if score >= minScore {
			matches = append(matches, searchMatch{Path: e.Path, IsDir: e.IsDir, score: score})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].IsDir != matches[j].IsDir {
			return !matches[i].IsDir // files before folders at equal score
		}
		return len(matches[i].Path) < len(matches[j].Path)
	})
	return matches
}

var reSplitTokens = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// tokenize lowercases the query and splits it into search words, dropping
// single-character noise.
func tokenize(s string) []string {
	parts := reSplitTokens.Split(strings.ToLower(s), -1)
	var out []string
	for _, p := range parts {
		if len([]rune(p)) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

// pathLeaf returns the final path segment, ignoring a trailing slash on dirs.
func pathLeaf(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ensureIndex returns the cached tree, rebuilding it when forced, when the
// in-memory copy is stale, or when no fresh on-disk copy exists.
func (m *MailRuCloud) ensureIndex(ctx context.Context, force bool) ([]indexEntry, time.Time, error) {
	m.indexMu.Lock()
	defer m.indexMu.Unlock()

	if !force {
		if m.index != nil && time.Since(m.indexTime) < indexTTL {
			return m.index, m.indexTime, nil
		}
		if m.index == nil {
			if entries, builtAt, ok := m.loadIndex(); ok && time.Since(builtAt) < indexTTL {
				m.index = entries
				m.indexTime = builtAt
				return entries, builtAt, nil
			}
		}
	}

	entries, err := m.buildIndex(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := time.Now()
	m.index = entries
	m.indexTime = now
	m.saveIndex(entries, now)
	return entries, now, nil
}

// buildIndex walks the whole tree under basePath breadth-first, collecting every
// file and folder path. Each BFS level is fetched concurrently (bounded worker
// pool) because a sequential PROPFIND-per-folder walk over a large tree takes many
// minutes. Unreadable folders are skipped rather than failing the whole walk.
func (m *MailRuCloud) buildIndex(ctx context.Context) ([]indexEntry, error) {
	const workers = 8

	var entries []indexEntry
	level := []string{"/"} // relative paths of folders to scan at the current depth

	for depth := 0; depth <= indexMaxDepth && len(level) > 0 && len(entries) < indexMaxEntries; depth++ {
		children := make([][]davChild, len(level))
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for i, rel := range level {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, rel string) {
				defer wg.Done()
				defer func() { <-sem }()
				if c, err := m.propfindChildren(ctx, rel); err == nil {
					children[i] = c
				}
			}(i, rel)
		}
		wg.Wait()

		var next []string
		for i, rel := range level {
			for _, c := range children[i] {
				child := rel + c.name
				if c.isDir {
					child += "/"
				}
				entries = append(entries, indexEntry{Path: child, IsDir: c.isDir})
				if c.isDir {
					next = append(next, child)
				}
			}
		}
		level = next
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("index is empty (cloud unreachable or no files)")
	}
	return entries, nil
}

type davChild struct {
	name  string
	isDir bool
}

func (m *MailRuCloud) propfindChildren(ctx context.Context, rel string) ([]davChild, error) {
	path := m.basePath + strings.TrimPrefix(rel, "/")
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	reqURL := "https://webdav.cloud.mail.ru" + encodeWebDAVPath(path)

	req, err := http.NewRequestWithContext(ctx, "PROPFIND", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(m.email, m.password)
	req.Header.Set("Depth", "1")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("WebDAV error: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseDavChildren(string(body)), nil
}

var (
	reDavResponse    = regexp.MustCompile(`(?s)<d:response>(.*?)</d:response>`)
	reDavDisplayname = regexp.MustCompile(`<d:displayname>([^<]*)</d:displayname>`)
)

// parseDavChildren extracts child entries from a Depth:1 PROPFIND body. The first
// <d:response> block is the requested folder itself and is skipped. A folder is
// identified by a <d:collection/> marker inside its resourcetype.
func parseDavChildren(body string) []davChild {
	blocks := reDavResponse.FindAllStringSubmatch(body, -1)
	var out []davChild
	for i, b := range blocks {
		if i == 0 {
			continue
		}
		chunk := b[1]
		dm := reDavDisplayname.FindStringSubmatch(chunk)
		if dm == nil || dm[1] == "" {
			continue
		}
		out = append(out, davChild{
			name:  dm[1],
			isDir: strings.Contains(chunk, "<d:collection"),
		})
	}
	return out
}

func (m *MailRuCloud) indexFilePath() string {
	return filepath.Join(m.filesDir, "mailru_index.json")
}

func (m *MailRuCloud) saveIndex(entries []indexEntry, builtAt time.Time) {
	data, err := json.Marshal(struct {
		BuiltAt time.Time    `json:"built_at"`
		Entries []indexEntry `json:"entries"`
	}{builtAt, entries})
	if err != nil {
		return
	}
	os.MkdirAll(m.filesDir, 0755)
	os.WriteFile(m.indexFilePath(), data, 0644)
}

func (m *MailRuCloud) loadIndex() ([]indexEntry, time.Time, bool) {
	data, err := os.ReadFile(m.indexFilePath())
	if err != nil {
		return nil, time.Time{}, false
	}
	var stored struct {
		BuiltAt time.Time    `json:"built_at"`
		Entries []indexEntry `json:"entries"`
	}
	if json.Unmarshal(data, &stored) != nil || len(stored.Entries) == 0 {
		return nil, time.Time{}, false
	}
	return stored.Entries, stored.BuiltAt, true
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

	// Save to the per-instance local files directory — sanitize filename.
	downloadDir := m.filesDir
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
