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
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// publicLinkRe matches a Mail.ru Cloud share link. Yuri sends these instead of
// paths: the folder lives in someone else's cloud, so there is no path inside
// the bot's own account to speak of.
var publicLinkRe = regexp.MustCompile(`(?i)https?://cloud\.mail\.ru/public/([^\s?#]+)`)

// ParsePublicLink extracts the weblink token from a share URL. A bare token
// ("dHhi/h2Sg1vzRe") is accepted too, so the model can pass either form.
func ParsePublicLink(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := publicLinkRe.FindStringSubmatch(s); m != nil {
		return strings.Trim(m[1], "/"), true
	}
	// A token is two slash-separated parts and never looks like a cloud path.
	if !strings.HasPrefix(s, "/") && strings.Count(s, "/") == 1 && !strings.Contains(s, " ") && s != "" {
		return s, true
	}
	return "", false
}

type publicEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Weblink string `json:"weblink"`
	Size    int64  `json:"size"`
}

type publicFolder struct {
	Body struct {
		Name string        `json:"name"`
		List []publicEntry `json:"list"`
	} `json:"body"`
}

const (
	publicAPI       = "https://cloud.mail.ru/api/v2"
	publicPageLimit = 500
	publicMaxDepth  = 6
)

// PublicFolderName reports what the shared folder is called, so a project folder
// can be named after the object rather than after an opaque token.
func (m *MailRuCloud) PublicFolderName(ctx context.Context, token string) (string, error) {
	folder, err := m.publicList(ctx, token)
	if err != nil {
		return "", err
	}
	return folder.Body.Name, nil
}

func (m *MailRuCloud) publicList(ctx context.Context, token string) (*publicFolder, error) {
	endpoint := fmt.Sprintf("%s/folder?weblink=%s&limit=%d", publicAPI, url.QueryEscape(token), publicPageLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("public folder: %w", err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("public folder %s: %w", token, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public folder %s: ответ %s — проверьте ссылку, она может быть закрыта", token, resp.Status)
	}

	var folder publicFolder
	if err := json.NewDecoder(resp.Body).Decode(&folder); err != nil {
		return nil, fmt.Errorf("public folder %s: %w", token, err)
	}
	return &folder, nil
}

// publicDownloadBase asks the dispatcher which host serves shared files. The
// host rotates, so it is resolved per run rather than hardcoded.
func (m *MailRuCloud) publicDownloadBase(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicAPI+"/dispatcher", nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatcher: %w", err)
	}
	defer resp.Body.Close()

	var d struct {
		Body struct {
			WeblinkGet []struct {
				URL string `json:"url"`
			} `json:"weblink_get"`
		} `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", fmt.Errorf("dispatcher: %w", err)
	}
	if len(d.Body.WeblinkGet) == 0 {
		return "", fmt.Errorf("dispatcher: нет адреса для скачивания публичных файлов")
	}
	return strings.TrimRight(d.Body.WeblinkGet[0].URL, "/"), nil
}

// CollectPublic downloads a shared folder into the local cache and returns the
// local paths, mirroring CollectFolder so the import path stays the same for
// both a private cloud path and a share link.
func (m *MailRuCloud) CollectPublic(ctx context.Context, link string, exts []string, maxFiles int) ([]string, error) {
	token, ok := ParsePublicLink(link)
	if !ok {
		return nil, fmt.Errorf("%q не похоже на публичную ссылку облака", link)
	}
	base, err := m.publicDownloadBase(ctx)
	if err != nil {
		return nil, err
	}

	var wanted []publicEntry
	if err := m.walkPublic(ctx, token, exts, publicMaxDepth, &wanted); err != nil {
		return nil, err
	}
	if maxFiles > 0 && len(wanted) > maxFiles {
		return nil, fmt.Errorf("в папке %d подходящих файлов — больше лимита %d, укажите вложенную папку",
			len(wanted), maxFiles)
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("в публичной папке не нашлось подходящих документов")
	}

	root := strings.Trim(token, "/")
	local := make([]string, 0, len(wanted))
	for _, e := range wanted {
		rel := strings.TrimPrefix(strings.Trim(e.Weblink, "/"), root)
		dest := filepath.Join(m.filesDir, "public", filepath.FromSlash(strings.Trim(rel, "/")))
		if err := m.fetchPublicFile(ctx, base, e.Weblink, dest); err != nil {
			slog.Warn("public collect: download failed", "file", e.Name, "error", err)
			continue // один файл не рвёт перенос
		}
		local = append(local, dest)
	}
	if len(local) == 0 {
		return nil, fmt.Errorf("не удалось скачать ни одного файла из публичной папки")
	}
	return local, nil
}

func (m *MailRuCloud) walkPublic(ctx context.Context, token string, exts []string, depth int, out *[]publicEntry) error {
	if depth <= 0 {
		return nil
	}
	folder, err := m.publicList(ctx, token)
	if err != nil {
		return err
	}
	for _, e := range folder.Body.List {
		if e.Type == "folder" {
			if err := m.walkPublic(ctx, e.Weblink, exts, depth-1, out); err != nil {
				return err
			}
			continue
		}
		if extAllowed(e.Name, exts) || isArchive(e.Name) {
			*out = append(*out, e)
		}
	}
	return nil
}

func (m *MailRuCloud) fetchPublicFile(ctx context.Context, base, weblink, destPath string) error {
	if fi, err := statFile(destPath); err == nil && fi > 0 {
		return nil // уже в кэше
	}
	// Each path segment is escaped separately: the names are Russian and full of
	// spaces, and escaping the slashes would break the URL.
	var parts []string
	for _, p := range strings.Split(strings.Trim(weblink, "/"), "/") {
		parts = append(parts, url.PathEscape(p))
	}
	endpoint := base + "/" + path.Join(parts...)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ответ %s", resp.Status)
	}
	return writeStream(destPath, resp.Body)
}

// statFile reports a file's size, or an error when it is missing.
func statFile(p string) (int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// writeStream saves a body through a ".part" file, so an interrupted download is
// never mistaken for a cached copy on the next run.
func writeStream(destPath string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	part := destPath + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(part)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, destPath)
}
