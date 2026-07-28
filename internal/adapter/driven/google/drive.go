package google

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const folderMime = "application/vnd.google-apps.folder"

// FileInfo is the trimmed view of a Drive entry the assistant needs. The SDK
// type carries dozens of fields that would only bloat tool results.
type FileInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
	IsFolder bool   `json:"is_folder"`
}

// Drive wraps the Drive v3 service, pinned to a single shared drive. Pinning is
// the security boundary: the service account is a member of this drive only, so
// no query can reach anyone's personal files.
type Drive struct {
	svc  *drive.Service
	root string
}

func NewDrive(ctx context.Context, rootFolderID string, opts ...option.ClientOption) (*Drive, error) {
	if rootFolderID == "" {
		return nil, fmt.Errorf("drive: root folder id is required")
	}
	svc, err := drive.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("drive service: %w", err)
	}
	return &Drive{svc: svc, root: rootFolderID}, nil
}

// Root is the shared drive this adapter is pinned to.
func (d *Drive) Root() string { return d.root }

const fileFields = "nextPageToken,files(id,name,mimeType,size,modifiedTime)"

// list runs a query against the shared drive. Every shared-drive parameter is
// mandatory: omit one and the API returns an empty list rather than an error,
// which reads as "the folder is empty" and is nearly impossible to debug.
func (d *Drive) list(ctx context.Context, q string) ([]FileInfo, error) {
	call := d.svc.Files.List().
		Q(q).
		Fields(fileFields).
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		Corpora("drive").
		DriveId(d.root).
		PageSize(200)

	var out []FileInfo
	err := call.Pages(ctx, func(page *drive.FileList) error {
		for _, f := range page.Files {
			out = append(out, toFileInfo(f))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("drive list: %w", err)
	}
	return out, nil
}

func toFileInfo(f *drive.File) FileInfo {
	return FileInfo{
		ID:       f.Id,
		Name:     f.Name,
		MimeType: f.MimeType,
		Size:     f.Size,
		Modified: f.ModifiedTime,
		IsFolder: f.MimeType == folderMime,
	}
}

// escapeQuery makes a string safe to embed in a Drive query literal.
func escapeQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`)
}

func (d *Drive) childrenQuery(parentID string) string {
	return fmt.Sprintf("'%s' in parents and trashed = false", escapeQuery(parentID))
}

func (d *Drive) folderQuery(parentID, name string) string {
	return fmt.Sprintf("'%s' in parents and name = '%s' and mimeType = '%s' and trashed = false",
		escapeQuery(parentID), escapeQuery(name), folderMime)
}

// List returns the contents of a folder. An empty id means the drive root.
func (d *Drive) List(ctx context.Context, folderID string) ([]FileInfo, error) {
	if folderID == "" {
		folderID = d.root
	}
	return d.list(ctx, d.childrenQuery(folderID))
}

func (d *Drive) Search(ctx context.Context, text string) ([]FileInfo, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("drive search: empty query")
	}
	return d.list(ctx, fmt.Sprintf("name contains '%s' and trashed = false", escapeQuery(text)))
}

// googleNativePrefix marks formats that exist only inside Google and therefore
// have no bytes to download; they must be exported to a real format instead.
const googleNativePrefix = "application/vnd.google-apps."

// exportFormat is the text form each native type is converted to. Text keeps
// documents readable by the model and by pdftotext-style tooling downstream.
func exportFormat(mimeType string) string {
	switch mimeType {
	case googleNativePrefix + "spreadsheet":
		return "text/csv"
	default:
		return "text/plain"
	}
}

func (d *Drive) Download(ctx context.Context, fileID string) ([]byte, error) {
	if fileID == "" {
		return nil, fmt.Errorf("drive download: file id is required")
	}

	meta, err := d.svc.Files.Get(fileID).Fields("mimeType").
		SupportsAllDrives(true).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("drive download: read type of %s: %w", fileID, err)
	}
	if strings.HasPrefix(meta.MimeType, googleNativePrefix) {
		resp, err := d.svc.Files.Export(fileID, exportFormat(meta.MimeType)).Context(ctx).Download()
		if err != nil {
			return nil, fmt.Errorf("drive export %s: %w", fileID, err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("drive export %s: %w", fileID, err)
		}
		return data, nil
	}

	resp, err := d.svc.Files.Get(fileID).SupportsAllDrives(true).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("drive download %s: %w", fileID, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("drive download %s: %w", fileID, err)
	}
	return data, nil
}

func (d *Drive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (FileInfo, error) {
	if name == "" {
		return FileInfo{}, fmt.Errorf("drive upload: name is required")
	}
	if parentID == "" {
		parentID = d.root
	}

	meta := &drive.File{Name: name, Parents: []string{parentID}}
	if mimeType != "" {
		meta.MimeType = mimeType
	}
	f, err := d.svc.Files.Create(meta).
		Media(bytes.NewReader(content)).
		Fields("id,name,mimeType,size,modifiedTime").
		SupportsAllDrives(true).
		Context(ctx).Do()
	if err != nil {
		return FileInfo{}, fmt.Errorf("drive upload %s: %w", name, err)
	}
	return toFileInfo(f), nil
}

// docMime is Google's native document type. Uploading text under it makes
// Drive convert the content, which is how a Doc gets created through this API.
const docMime = googleNativePrefix + "document"

// CreateDoc writes text as a Google Doc. Proposals and roadmaps go to clients
// and get edited together, which a plain file in Drive does not support.
func (d *Drive) CreateDoc(ctx context.Context, parentID, name, text string) (FileInfo, error) {
	if name == "" {
		return FileInfo{}, fmt.Errorf("drive: document name is required")
	}
	if parentID == "" {
		parentID = d.root
	}

	meta := &drive.File{Name: name, MimeType: docMime, Parents: []string{parentID}}
	f, err := d.svc.Files.Create(meta).
		Media(strings.NewReader(text), googleapi.ContentType("text/plain")).
		Fields("id,name,mimeType,modifiedTime").
		SupportsAllDrives(true).
		Context(ctx).Do()
	if err != nil {
		return FileInfo{}, fmt.Errorf("drive create doc %s: %w", name, err)
	}
	return toFileInfo(f), nil
}

// EnsureFolder returns the existing folder with this name or creates it.
// Idempotent by design: project scaffolding re-runs after partial failures, and
// a second run must not leave two folders with the same name.
func (d *Drive) EnsureFolder(ctx context.Context, parentID, name string) (FileInfo, error) {
	if name == "" {
		return FileInfo{}, fmt.Errorf("drive: folder name is required")
	}
	if parentID == "" {
		parentID = d.root
	}

	found, err := d.list(ctx, d.folderQuery(parentID, name))
	if err != nil {
		return FileInfo{}, err
	}
	if len(found) > 0 {
		return found[0], nil
	}
	return d.createFolder(ctx, parentID, name)
}

// createFolder skips the existence check. Callers that have already looked the
// folder up use this to avoid paying for the same query twice.
func (d *Drive) createFolder(ctx context.Context, parentID, name string) (FileInfo, error) {
	meta := &drive.File{Name: name, MimeType: folderMime, Parents: []string{parentID}}
	f, err := d.svc.Files.Create(meta).
		Fields("id,name,mimeType,modifiedTime").
		SupportsAllDrives(true).
		Context(ctx).Do()
	if err != nil {
		return FileInfo{}, fmt.Errorf("drive create folder %s: %w", name, err)
	}
	return toFileInfo(f), nil
}

// EnsurePath resolves a path, creating folders that do not exist yet — except
// the first segment, which must already be there. The tool loop executes a
// batch of tool calls in parallel, so a model's "create the folder, then move
// files into it" cannot rely on ordering; ensuring the path on write makes the
// order irrelevant. Refusing to create the first segment keeps a typo in the
// project name a loud error instead of a silently started second project.
func (d *Drive) EnsurePath(ctx context.Context, path string) (string, error) {
	current := d.root
	for i, part := range splitPath(path) {
		found, err := d.list(ctx, d.folderQuery(current, part))
		if err != nil {
			return "", err
		}
		if len(found) > 0 {
			current = found[0].ID
			continue
		}
		if i == 0 {
			return "", fmt.Errorf("drive: project folder %q not found — create it first or check the name", part)
		}
		created, err := d.createFolder(ctx, current, part)
		if err != nil {
			return "", err
		}
		current = created.ID
	}
	return current, nil
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range strings.Split(strings.Trim(path, "/"), "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// Move reparents a file. Drive has no move operation: a file's location is its
// parent list, so the current parents are read first and swapped for the new
// one. Copy-and-delete would work too but would break every existing link to
// the file, and links are how the registry points at documents.
func (d *Drive) Move(ctx context.Context, fileID, newParentID string) (FileInfo, error) {
	if fileID == "" {
		return FileInfo{}, fmt.Errorf("drive move: file id is required")
	}
	if newParentID == "" {
		return FileInfo{}, fmt.Errorf("drive move: target folder id is required")
	}

	current, err := d.svc.Files.Get(fileID).Fields("parents").
		SupportsAllDrives(true).Context(ctx).Do()
	if err != nil {
		return FileInfo{}, fmt.Errorf("drive move: read parents of %s: %w", fileID, err)
	}

	call := d.svc.Files.Update(fileID, nil).
		AddParents(newParentID).
		Fields("id,name,mimeType,size,modifiedTime").
		SupportsAllDrives(true)
	if len(current.Parents) > 0 {
		call = call.RemoveParents(strings.Join(current.Parents, ","))
	}

	f, err := call.Context(ctx).Do()
	if err != nil {
		return FileInfo{}, fmt.Errorf("drive move %s: %w", fileID, err)
	}
	return toFileInfo(f), nil
}

// ResolvePath walks a slash-separated path from the drive root and returns the
// folder id. An empty path resolves to the root without any API call.
func (d *Drive) ResolvePath(ctx context.Context, path string) (string, error) {
	current := d.root
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		found, err := d.list(ctx, d.folderQuery(current, part))
		if err != nil {
			return "", err
		}
		if len(found) == 0 {
			return "", fmt.Errorf("drive: folder %q not found in path %q", part, path)
		}
		current = found[0].ID
	}
	return current, nil
}
