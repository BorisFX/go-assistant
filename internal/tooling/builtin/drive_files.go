package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// DriveClient is the slice of the Drive adapter this tool needs. Declaring it
// here lets tests substitute a fake without pulling in the Google SDK.
type DriveClient interface {
	List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error)
	Search(ctx context.Context, text string) ([]gworkspace.FileInfo, error)
	Download(ctx context.Context, fileID string) ([]byte, error)
	Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error)
	EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error)
	Move(ctx context.Context, fileID, newParentID string) (gworkspace.FileInfo, error)
	ResolvePath(ctx context.Context, path string) (string, error)
}

// DriveFiles exposes the project shared drive to the model. The action-dispatch
// shape mirrors cloud_files so both storages present the same contract.
type DriveFiles struct {
	client   DriveClient
	filesDir string
}

func NewDriveFiles(client DriveClient, filesDir string) *DriveFiles {
	return &DriveFiles{client: client, filesDir: filesDir}
}

func (d *DriveFiles) Name() string { return "drive_files" }

func (d *DriveFiles) Description() string {
	return "Google Drive project workspace: list, search, read, download, upload files and create folders"
}

func (d *DriveFiles) Category() string { return "files" }

func (d *DriveFiles) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "search", "read", "download", "upload", "mkdir", "move"],
				"description": "Operation to perform"
			},
			"path": {"type": "string", "description": "Folder path from the drive root, e.g. Vertex/03_Техпланы. Empty means the root. For move it is the destination folder"},
			"file_id": {"type": "string", "description": "Drive file id, for read, download and move"},
			"query": {"type": "string", "description": "Text matched against file names, for search"},
			"name": {"type": "string", "description": "File or folder name, for upload, mkdir and download"},
			"content": {"type": "string", "description": "Text content, for upload"}
		},
		"required": ["action"]
	}`)
}

type driveFilesParams struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	FileID  string `json:"file_id"`
	Query   string `json:"query"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (d *DriveFiles) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p driveFilesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	switch p.Action {
	case "list":
		return d.list(ctx, p.Path)
	case "search":
		return d.search(ctx, p.Query)
	case "read":
		return d.read(ctx, p.FileID)
	case "download":
		return d.download(ctx, p.FileID, p.Name)
	case "upload":
		return d.upload(ctx, p.Path, p.Name, p.Content)
	case "mkdir":
		return d.mkdir(ctx, p.Path, p.Name)
	case "move":
		return d.move(ctx, p.FileID, p.Path)
	default:
		return nil, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (d *DriveFiles) list(ctx context.Context, path string) (json.RawMessage, error) {
	folderID, err := d.client.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	files, err := d.client.List(ctx, folderID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"path": path, "files": files})
}

func (d *DriveFiles) search(ctx context.Context, query string) (json.RawMessage, error) {
	files, err := d.client.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"query": query, "files": files})
}

// maxInlineRead caps what goes straight into the conversation. Larger documents
// belong in download, which hands the file to the extraction pipeline instead.
const maxInlineRead = 100_000

func (d *DriveFiles) read(ctx context.Context, fileID string) (json.RawMessage, error) {
	if fileID == "" {
		return nil, fmt.Errorf("read: file_id is required")
	}
	data, err := d.client.Download(ctx, fileID)
	if err != nil {
		return nil, err
	}

	content := string(data)
	truncated := false
	if len(content) > maxInlineRead {
		content = content[:maxInlineRead]
		truncated = true
	}
	return json.Marshal(map[string]any{"content": content, "truncated": truncated})
}

// download saves the file locally so other tools, such as document extraction,
// can work with a real path. Mirrors how cloud_files behaves.
func (d *DriveFiles) download(ctx context.Context, fileID, name string) (json.RawMessage, error) {
	if fileID == "" {
		return nil, fmt.Errorf("download: file_id is required")
	}
	data, err := d.client.Download(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = fileID
	}
	if err := os.MkdirAll(d.filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create files dir: %w", err)
	}

	// Base only: the name comes from the model and must not escape filesDir.
	dest := filepath.Join(d.filesDir, filepath.Base(name))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return nil, fmt.Errorf("save file: %w", err)
	}
	return json.Marshal(map[string]any{"path": dest, "size": len(data)})
}

func mimeForName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}

func (d *DriveFiles) upload(ctx context.Context, path, name, content string) (json.RawMessage, error) {
	if name == "" {
		return nil, fmt.Errorf("upload: name is required")
	}
	folderID, err := d.client.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	info, err := d.client.Upload(ctx, folderID, name, mimeForName(name), []byte(content))
	if err != nil {
		return nil, err
	}
	return json.Marshal(info)
}

func (d *DriveFiles) mkdir(ctx context.Context, path, name string) (json.RawMessage, error) {
	if name == "" {
		return nil, fmt.Errorf("mkdir: name is required")
	}
	parentID, err := d.client.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	info, err := d.client.EnsureFolder(ctx, parentID, name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(info)
}

// move relocates a file into the folder at path. Used to sort documents out of
// _Разобрать into the stage folder they belong to.
func (d *DriveFiles) move(ctx context.Context, fileID, path string) (json.RawMessage, error) {
	if fileID == "" {
		return nil, fmt.Errorf("move: file_id is required")
	}
	target, err := d.client.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	info, err := d.client.Move(ctx, fileID, target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"moved": info, "to": path})
}
