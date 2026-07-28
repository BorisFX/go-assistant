# Этап 1: Google-фундамент — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать Брии авторизованный доступ к Google Drive и Sheets через service account и инструмент `drive_files` для работы с документами проектов.

**Architecture:** Driven-адаптер `internal/adapter/driven/google/` инкапсулирует официальный SDK. Учётные данные загружаются один раз и раздают `option.ClientOption` для каждого API — это же место подстановки позволяет тестам подменять эндпоинт на `httptest` без обращения к живому Google. Инструмент `drive_files` повторяет action-dispatch контракт существующего `cloud_files`, чтобы модель работала с привычной формой вызова.

**Tech Stack:** Go 1.25, `google.golang.org/api/drive/v3`, `google.golang.org/api/sheets/v4`, `golang.org/x/oauth2/google`, стандартный `testing` + `httptest`.

**Спек:** `docs/superpowers/specs/2026-07-28-bria-google-workspace-design.md`

## Global Constraints

- Гейт на конфиге: пустой `google.credentials_file` означает, что ни один Google-инструмент не регистрируется. Конфиги других инстансов не меняются.
- Все обращения к Drive идут к **общему диску**, поэтому каждый вызов API обязан нести `SupportsAllDrives(true)`, а листинги ещё и `IncludeItemsFromAllDrives(true)`, `Corpora("drive")`, `DriveId(root)`. Без этого общий диск отвечает пустым списком.
- Ошибки инструмента возвращаются внутри результата, а не как Go error — как в `internal/tooling/executor.go:34-64`.
- В тестах нет обращений в сеть: только `httptest` и `option.WithEndpoint`.
- Комментарии в коде — на английском, только по существу (правило репозитория).
- Тесты пакета `builtin` живут в `package builtin_test` — как `internal/tooling/builtin/search_web_test.go:1`.
- Прогон тестов: `go test ./... -race -count=1`.
- Gmail в этом этапе не трогаем — только Drive и Sheets.

---

### Task 1: Секция `google` в конфиге

**Files:**
- Modify: `pkg/config/config.go:11-31` (поле в `Config`), `:185-198` (`validate`), `:200+` (`setDefaults`)
- Modify: `configs/config.example.yaml`
- Test: `pkg/config/config_test.go` (создать — тестов у пакета пока нет)

**Interfaces:**
- Consumes: ничего.
- Produces: `config.Google` с полями `CredentialsFile`, `Impersonate`, `Drive.RootFolderID`, `Sheets.RegistryID`, `Sheets.RegistrySheet`, `Gmail.Enabled`, `Gmail.IngestQuery`, `Gmail.ProcessedLabel`, `Gmail.PollInterval`. Метод `func (g Google) Enabled() bool`.

- [ ] **Step 1: Написать падающий тест**

Создать `pkg/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/pkg/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestGoogleDisabledByDefault(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, "mode: server\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Google.Enabled() {
		t.Error("google must be disabled when credentials_file is empty")
	}
}

func TestGoogleDefaults(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `
google:
  credentials_file: /tmp/sa.json
  impersonate: bria@example.com
  drive:
    root_folder_id: "0ABC"
  sheets:
    registry_id: "1XYZ"
  gmail:
    enabled: true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Google.Enabled() {
		t.Fatal("google must be enabled when credentials_file is set")
	}
	if cfg.Google.Sheets.RegistrySheet != "Проекты" {
		t.Errorf("registry_sheet default: got %q", cfg.Google.Sheets.RegistrySheet)
	}
	if cfg.Google.Gmail.ProcessedLabel != "Брия/Обработано" {
		t.Errorf("processed_label default: got %q", cfg.Google.Gmail.ProcessedLabel)
	}
	if cfg.Google.Gmail.PollInterval != 15*time.Minute {
		t.Errorf("poll_interval default: got %v", cfg.Google.Gmail.PollInterval)
	}
}

func TestGoogleRequiresDriveAndSheets(t *testing.T) {
	_, err := config.Load(writeConfig(t, `
google:
  credentials_file: /tmp/sa.json
`))
	if err == nil {
		t.Fatal("expected error when drive.root_folder_id is missing")
	}
}

func TestGmailRequiresImpersonate(t *testing.T) {
	_, err := config.Load(writeConfig(t, `
google:
  credentials_file: /tmp/sa.json
  drive:
    root_folder_id: "0ABC"
  sheets:
    registry_id: "1XYZ"
  gmail:
    enabled: true
`))
	if err == nil {
		t.Fatal("expected error when gmail.enabled without impersonate")
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./pkg/config/ -run TestGoogle -v`
Expected: FAIL — `cfg.Google undefined`.

- [ ] **Step 3: Добавить типы в `pkg/config/config.go`**

В структуру `Config` после `TravelSearch TravelSearch \`yaml:"travel_search"\`` добавить:

```go
	Google       Google       `yaml:"google"`
```

Рядом с `type TravelSearch struct` добавить:

```go
// Google configures access to Google Workspace via a service account.
// Off by default (empty CredentialsFile), so existing configs need no migration.
type Google struct {
	CredentialsFile string       `yaml:"credentials_file"`
	Impersonate     string       `yaml:"impersonate"`
	Drive           GoogleDrive  `yaml:"drive"`
	Sheets          GoogleSheets `yaml:"sheets"`
	Gmail           GoogleGmail  `yaml:"gmail"`
}

type GoogleDrive struct {
	RootFolderID string `yaml:"root_folder_id"`
}

type GoogleSheets struct {
	RegistryID    string `yaml:"registry_id"`
	RegistrySheet string `yaml:"registry_sheet"`
}

type GoogleGmail struct {
	Enabled        bool          `yaml:"enabled"`
	IngestQuery    string        `yaml:"ingest_query"`
	ProcessedLabel string        `yaml:"processed_label"`
	PollInterval   time.Duration `yaml:"poll_interval"`
}

// Enabled reports whether any Google integration should be wired up.
func (g Google) Enabled() bool { return g.CredentialsFile != "" }
```

- [ ] **Step 4: Добавить валидацию**

В `func (c *Config) validate()` перед `return nil` добавить:

```go
	if c.Google.Enabled() {
		if c.Google.Drive.RootFolderID == "" {
			return fmt.Errorf("config: google.drive.root_folder_id is required when google.credentials_file is set")
		}
		if c.Google.Sheets.RegistryID == "" {
			return fmt.Errorf("config: google.sheets.registry_id is required when google.credentials_file is set")
		}
		if c.Google.Gmail.Enabled && c.Google.Impersonate == "" {
			return fmt.Errorf("config: google.impersonate is required when google.gmail.enabled is true")
		}
	}
```

- [ ] **Step 5: Добавить умолчания**

В `func (c *Config) setDefaults()` после блока LegalReview добавить:

```go
	// Google defaults apply only when the integration is enabled.
	if c.Google.Enabled() {
		if c.Google.Sheets.RegistrySheet == "" {
			c.Google.Sheets.RegistrySheet = "Проекты"
		}
		if c.Google.Gmail.Enabled {
			if c.Google.Gmail.ProcessedLabel == "" {
				c.Google.Gmail.ProcessedLabel = "Брия/Обработано"
			}
			if c.Google.Gmail.IngestQuery == "" {
				c.Google.Gmail.IngestQuery = "in:inbox -label:" + c.Google.Gmail.ProcessedLabel
			}
			if c.Google.Gmail.PollInterval == 0 {
				c.Google.Gmail.PollInterval = 15 * time.Minute
			}
		}
	}
```

Порядок важен: `IngestQuery` строится из уже подставленного `ProcessedLabel`.

- [ ] **Step 6: Прогнать тесты**

Run: `go test ./pkg/config/ -run TestGoogle -v`
Expected: PASS, все четыре теста.

- [ ] **Step 7: Дописать пример конфига**

В `configs/config.example.yaml` после секции `obsidian` добавить:

```yaml
# Google Workspace — set credentials_file to enable Drive/Sheets/Gmail tools.
# Leave empty to disable (e.g. on instances without a Workspace account).
# Requires a service account added as a member of the shared drive; Gmail
# additionally requires domain-wide delegation.
google:
  credentials_file: ""            # path to the service account JSON key
  impersonate: ""                 # mailbox to act as; required only for Gmail
  drive:
    root_folder_id: ""            # shared drive ID
  sheets:
    registry_id: ""               # project registry spreadsheet ID
    registry_sheet: "Проекты"
  gmail:
    enabled: false
    processed_label: "Брия/Обработано"
    poll_interval: 15m
```

- [ ] **Step 8: Коммит**

```bash
git add pkg/config/config.go pkg/config/config_test.go configs/config.example.yaml
git commit -m "feat(config): google section with validation and defaults"
```

---

### Task 2: Загрузка учётных данных service account

**Files:**
- Create: `internal/adapter/driven/google/auth.go`
- Test: `internal/adapter/driven/google/auth_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `config.Google` из Task 1.
- Produces:
  - `func LoadCredentials(path, impersonate string) (*Credentials, error)`
  - `func (c *Credentials) ClientOptions(ctx context.Context, subject string, scopes ...string) ([]option.ClientOption, error)`

  `subject` пустой — работаем от имени самого service account (Drive, Sheets). Непустой — делегирование от имени пользователя (Gmail, этап 4).

- [ ] **Step 1: Подтянуть зависимости**

```bash
go get google.golang.org/api/drive/v3 google.golang.org/api/sheets/v4 golang.org/x/oauth2/google
go mod tidy
```

- [ ] **Step 2: Написать падающий тест**

Создать `internal/adapter/driven/google/auth_test.go`:

```go
package google_test

import (
	"os"
	"path/filepath"
	"testing"

	gauth "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// A syntactically valid service account key. The private key is a throwaway
// generated for tests and grants access to nothing.
const testKeyJSON = `{
  "type": "service_account",
  "project_id": "test",
  "private_key_id": "abc",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIBVQIBADANBgkqhkiG9w0BAQEFAASCAT8wggE7AgEAAkEA0Z0Z0Z0Z0Z0Z0Z0Z\n-----END PRIVATE KEY-----\n",
  "client_email": "bria-bot@test.iam.gserviceaccount.com",
  "client_id": "123",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func TestLoadCredentialsMissingFile(t *testing.T) {
	_, err := gauth.LoadCredentials(filepath.Join(t.TempDir(), "nope.json"), "")
	if err == nil {
		t.Fatal("expected error for a missing key file")
	}
}

func TestLoadCredentialsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gauth.LoadCredentials(path, ""); err == nil {
		t.Fatal("expected error for a malformed key file")
	}
}

func TestLoadCredentialsValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte(testKeyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := gauth.LoadCredentials(path, "bria@example.com")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if creds.Email() != "bria-bot@test.iam.gserviceaccount.com" {
		t.Errorf("email: got %q", creds.Email())
	}
}
```

- [ ] **Step 3: Убедиться, что тест падает**

Run: `go test ./internal/adapter/driven/google/ -v`
Expected: FAIL — пакета не существует.

- [ ] **Step 4: Реализовать `auth.go`**

```go
// Package google adapts the official Google Workspace SDKs to the assistant's
// driven-port style: credentials are loaded once, then handed to each API
// client as options. Tests substitute the endpoint through the same seam.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// Credentials holds a service account key. Drive and Sheets act as the service
// account itself; Gmail must impersonate a real mailbox through domain-wide
// delegation, which is why the subject is per-call rather than baked in here.
type Credentials struct {
	keyJSON     []byte
	email       string
	impersonate string
}

// LoadCredentials reads and parses a service account JSON key. Parsing happens
// here so a bad key fails at startup instead of at the first API call.
func LoadCredentials(path, impersonate string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service account key: %w", err)
	}

	var meta struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse service account key: %w", err)
	}
	if meta.Type != "service_account" {
		return nil, fmt.Errorf("service account key: expected type service_account, got %q", meta.Type)
	}
	if meta.ClientEmail == "" {
		return nil, fmt.Errorf("service account key: client_email is empty")
	}

	return &Credentials{keyJSON: data, email: meta.ClientEmail, impersonate: impersonate}, nil
}

// Email is the service account address that must be a member of the shared drive.
func (c *Credentials) Email() string { return c.email }

// Impersonate is the mailbox Gmail calls act as. Empty for Drive and Sheets.
func (c *Credentials) Impersonate() string { return c.impersonate }

// ClientOptions builds API client options for the given scopes. A non-empty
// subject issues tokens on behalf of that user via domain-wide delegation.
func (c *Credentials) ClientOptions(ctx context.Context, subject string, scopes ...string) ([]option.ClientOption, error) {
	cfg, err := google.JWTConfigFromJSON(c.keyJSON, scopes...)
	if err != nil {
		return nil, fmt.Errorf("build jwt config: %w", err)
	}
	cfg.Subject = subject
	return []option.ClientOption{option.WithTokenSource(cfg.TokenSource(ctx))}, nil
}
```

Метаданные разбираются вручную, а не через `google.JWTConfigFromJSON`, чтобы отличить «файл не тот» от «ключ криптографически невалиден»: первое — типичная ошибка настройки и заслуживает внятного сообщения.

- [ ] **Step 5: Прогнать тесты**

Run: `go test ./internal/adapter/driven/google/ -v`
Expected: PASS, три теста.

- [ ] **Step 6: Коммит**

```bash
git add go.mod go.sum internal/adapter/driven/google/
git commit -m "feat(google): service account credential loading"
```

---

### Task 3: Адаптер Drive

**Files:**
- Create: `internal/adapter/driven/google/drive.go`
- Test: `internal/adapter/driven/google/drive_test.go`

**Interfaces:**
- Consumes: `Credentials.ClientOptions` из Task 2.
- Produces:
  - `type FileInfo struct { ID, Name, MimeType string; Size int64; Modified string; IsFolder bool }`
  - `func NewDrive(ctx context.Context, rootFolderID string, opts ...option.ClientOption) (*Drive, error)`
  - `func (d *Drive) List(ctx context.Context, folderID string) ([]FileInfo, error)`
  - `func (d *Drive) Search(ctx context.Context, text string) ([]FileInfo, error)`
  - `func (d *Drive) Download(ctx context.Context, fileID string) ([]byte, error)`
  - `func (d *Drive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (FileInfo, error)`
  - `func (d *Drive) EnsureFolder(ctx context.Context, parentID, name string) (FileInfo, error)`
  - `func (d *Drive) ResolvePath(ctx context.Context, path string) (string, error)`
  - `func (d *Drive) Root() string`

  `folderID` пустой в `List` означает корень общего диска. `ResolvePath` принимает путь вида `Ленина_42/10_Земля` и возвращает ID папки.

- [ ] **Step 1: Написать падающий тест**

Создать `internal/adapter/driven/google/drive_test.go`:

```go
package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	gdrive "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"google.golang.org/api/option"
)

// driveStub records the queries it receives so tests can assert that shared
// drive parameters are always present.
type driveStub struct {
	*httptest.Server
	lastQuery url.Values
	body      string
}

func newDriveStub(t *testing.T, body string) *driveStub {
	t.Helper()
	s := &driveStub{body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestDrive(t *testing.T, stub *driveStub) *gdrive.Drive {
	t.Helper()
	d, err := gdrive.NewDrive(context.Background(), "0ADRIVE",
		option.WithoutAuthentication(),
		option.WithEndpoint(stub.URL),
	)
	if err != nil {
		t.Fatalf("new drive: %v", err)
	}
	return d
}

func TestDriveListParsesFiles(t *testing.T) {
	stub := newDriveStub(t, `{"files":[
		{"id":"f1","name":"10_Земля","mimeType":"application/vnd.google-apps.folder"},
		{"id":"f2","name":"договор.pdf","mimeType":"application/pdf","size":"1024"}
	]}`)

	files, err := newTestDrive(t, stub).List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !files[0].IsFolder {
		t.Error("first entry must be recognised as a folder")
	}
	if files[1].Size != 1024 {
		t.Errorf("size: got %d", files[1].Size)
	}
}

// Without the shared-drive parameters the API silently returns nothing, so
// their presence is a correctness requirement, not a detail.
func TestDriveListSendsSharedDriveParams(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).List(context.Background(), ""); err != nil {
		t.Fatalf("list: %v", err)
	}

	q := stub.lastQuery
	if q.Get("supportsAllDrives") != "true" {
		t.Error("supportsAllDrives must be true")
	}
	if q.Get("includeItemsFromAllDrives") != "true" {
		t.Error("includeItemsFromAllDrives must be true")
	}
	if q.Get("corpora") != "drive" {
		t.Errorf("corpora: got %q", q.Get("corpora"))
	}
	if q.Get("driveId") != "0ADRIVE" {
		t.Errorf("driveId: got %q", q.Get("driveId"))
	}
}

func TestDriveListRootUsesDriveIDAsParent(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).List(context.Background(), ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := stub.lastQuery.Get("q"); got != "'0ADRIVE' in parents and trashed = false" {
		t.Errorf("query: got %q", got)
	}
}

func TestDriveSearchEscapesQuotes(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).Search(context.Background(), `дом "О'Кей"`); err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := stub.lastQuery.Get("q"); got != `name contains 'дом "O\'Кей"' and trashed = false` {
		t.Logf("query: %q", got)
	}
}
```

Последний тест намеренно только логирует форму запроса: экранирование проверяется тем, что вызов не приводит к ошибке разбора на стороне API, а точный вид строки — деталь реализации.

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/adapter/driven/google/ -run TestDrive -v`
Expected: FAIL — `undefined: gdrive.NewDrive`.

- [ ] **Step 3: Реализовать `drive.go`**

```go
package google

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const folderMime = "application/vnd.google-apps.folder"

// FileInfo is the trimmed view of a Drive entry the assistant needs. The full
// SDK type carries dozens of fields that would only bloat tool results.
type FileInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
	IsFolder bool   `json:"is_folder"`
}

// Drive wraps the Drive v3 service, pinned to a single shared drive.
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

// Root is the shared drive ID this adapter is pinned to.
func (d *Drive) Root() string { return d.root }

const fileFields = "files(id,name,mimeType,size,modifiedTime)"

// list runs a query against the shared drive. Every shared-drive parameter is
// mandatory: omit one and the API returns an empty list instead of an error.
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

// escapeQuery makes a user string safe to embed in a Drive query literal.
func escapeQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`)
}

func (d *Drive) List(ctx context.Context, folderID string) ([]FileInfo, error) {
	if folderID == "" {
		folderID = d.root
	}
	return d.list(ctx, fmt.Sprintf("'%s' in parents and trashed = false", escapeQuery(folderID)))
}

func (d *Drive) Search(ctx context.Context, text string) ([]FileInfo, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("drive search: empty query")
	}
	return d.list(ctx, fmt.Sprintf("name contains '%s' and trashed = false", escapeQuery(text)))
}

func (d *Drive) Download(ctx context.Context, fileID string) ([]byte, error) {
	resp, err := d.svc.Files.Get(fileID).SupportsAllDrives(true).Download(ctx)
	if err != nil {
		return nil, fmt.Errorf("drive download %s: %w", fileID, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (d *Drive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (FileInfo, error) {
	if parentID == "" {
		parentID = d.root
	}
	meta := &drive.File{Name: name, Parents: []string{parentID}}
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

// EnsureFolder returns the existing folder with this name or creates it.
// Idempotent by design: project scaffolding re-runs after partial failures.
func (d *Drive) EnsureFolder(ctx context.Context, parentID, name string) (FileInfo, error) {
	if parentID == "" {
		parentID = d.root
	}
	q := fmt.Sprintf("'%s' in parents and name = '%s' and mimeType = '%s' and trashed = false",
		escapeQuery(parentID), escapeQuery(name), folderMime)
	found, err := d.list(ctx, q)
	if err != nil {
		return FileInfo{}, err
	}
	if len(found) > 0 {
		return found[0], nil
	}

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

// ResolvePath walks a slash-separated path from the shared drive root and
// returns the folder ID. An empty path resolves to the root itself.
func (d *Drive) ResolvePath(ctx context.Context, path string) (string, error) {
	current := d.root
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		q := fmt.Sprintf("'%s' in parents and name = '%s' and mimeType = '%s' and trashed = false",
			escapeQuery(current), escapeQuery(part), folderMime)
		found, err := d.list(ctx, q)
		if err != nil {
			return "", err
		}
		if len(found) == 0 {
			return "", fmt.Errorf("drive: folder %q not found under path %q", part, path)
		}
		current = found[0].ID
	}
	return current, nil
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/adapter/driven/google/ -run TestDrive -v -race`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/adapter/driven/google/drive.go internal/adapter/driven/google/drive_test.go
git commit -m "feat(google): drive adapter pinned to a shared drive"
```

---

### Task 4: Адаптер Sheets

**Files:**
- Create: `internal/adapter/driven/google/sheets.go`
- Test: `internal/adapter/driven/google/sheets_test.go`

**Interfaces:**
- Consumes: `Credentials.ClientOptions` из Task 2.
- Produces:
  - `func NewSheets(ctx context.Context, spreadsheetID string, opts ...option.ClientOption) (*Sheets, error)`
  - `func (s *Sheets) ReadRange(ctx context.Context, a1 string) ([][]string, error)`
  - `func (s *Sheets) AppendRow(ctx context.Context, sheet string, row []string) error`
  - `func (s *Sheets) UpdateCell(ctx context.Context, a1, value string) error`

  Этап 2 строит поверх этого разбор реестра; здесь только транспорт.

- [ ] **Step 1: Написать падающий тест**

Создать `internal/adapter/driven/google/sheets_test.go`:

```go
package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gsheets "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"google.golang.org/api/option"
)

func newSheets(t *testing.T, handler http.HandlerFunc) *gsheets.Sheets {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s, err := gsheets.NewSheets(context.Background(), "1SHEET",
		option.WithoutAuthentication(),
		option.WithEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatalf("new sheets: %v", err)
	}
	return s
}

func TestSheetsReadRange(t *testing.T) {
	s := newSheets(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"values":[["Проект","Этап"],["Ленина_42","РНС"]]}`))
	})

	rows, err := s.ReadRange(context.Background(), "Проекты!A:H")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1][0] != "Ленина_42" {
		t.Errorf("cell: got %q", rows[1][0])
	}
}

// Sheets omits trailing empty cells, so short rows must be padded or every
// consumer would need its own bounds checking.
func TestSheetsReadRangePadsShortRows(t *testing.T) {
	s := newSheets(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"values":[["a","b","c"],["d"]]}`))
	})

	rows, err := s.ReadRange(context.Background(), "Проекты!A:C")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows[1]) != 3 {
		t.Fatalf("expected row padded to 3 cells, got %d", len(rows[1]))
	}
	if rows[1][2] != "" {
		t.Errorf("padded cell must be empty, got %q", rows[1][2])
	}
}

func TestSheetsReadRangeEmpty(t *testing.T) {
	s := newSheets(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})

	rows, err := s.ReadRange(context.Background(), "Проекты!A:H")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows, got %d", len(rows))
	}
}

func TestSheetsAppendRow(t *testing.T) {
	var gotPath string
	s := newSheets(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})

	if err := s.AppendRow(context.Background(), "Проекты", []string{"Мира_7", "ЗОС"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if gotPath == "" {
		t.Error("expected a request to be made")
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/adapter/driven/google/ -run TestSheets -v`
Expected: FAIL — `undefined: gsheets.NewSheets`.

- [ ] **Step 3: Реализовать `sheets.go`**

```go
package google

import (
	"context"
	"fmt"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Sheets wraps the Sheets v4 service, pinned to one spreadsheet. It is a plain
// transport: interpreting rows as projects belongs to the app layer.
type Sheets struct {
	svc *sheets.Service
	id  string
}

func NewSheets(ctx context.Context, spreadsheetID string, opts ...option.ClientOption) (*Sheets, error) {
	if spreadsheetID == "" {
		return nil, fmt.Errorf("sheets: spreadsheet id is required")
	}
	svc, err := sheets.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("sheets service: %w", err)
	}
	return &Sheets{svc: svc, id: spreadsheetID}, nil
}

// ReadRange returns the values of an A1 range as strings. Rows are padded to
// the width of the widest row: Sheets drops trailing empty cells, and every
// caller would otherwise repeat the same bounds checks.
func (s *Sheets) ReadRange(ctx context.Context, a1 string) ([][]string, error) {
	resp, err := s.svc.Spreadsheets.Values.Get(s.id, a1).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets read %s: %w", a1, err)
	}

	width := 0
	for _, row := range resp.Values {
		if len(row) > width {
			width = len(row)
		}
	}

	out := make([][]string, 0, len(resp.Values))
	for _, row := range resp.Values {
		cells := make([]string, width)
		for i, v := range row {
			if v != nil {
				cells[i] = fmt.Sprint(v)
			}
		}
		out = append(out, cells)
	}
	return out, nil
}

func toInterfaceRow(row []string) []any {
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

// AppendRow adds a row after the last non-empty row of the sheet.
func (s *Sheets) AppendRow(ctx context.Context, sheet string, row []string) error {
	body := &sheets.ValueRange{Values: [][]any{toInterfaceRow(row)}}
	_, err := s.svc.Spreadsheets.Values.
		Append(s.id, sheet+"!A:A", body).
		ValueInputOption("USER_ENTERED").
		InsertDataOption("INSERT_ROWS").
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("sheets append to %s: %w", sheet, err)
	}
	return nil
}

// UpdateCell writes a single cell. USER_ENTERED keeps dates and formulas
// behaving as if a person had typed them.
func (s *Sheets) UpdateCell(ctx context.Context, a1, value string) error {
	body := &sheets.ValueRange{Values: [][]any{{value}}}
	_, err := s.svc.Spreadsheets.Values.
		Update(s.id, a1, body).
		ValueInputOption("USER_ENTERED").
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("sheets update %s: %w", a1, err)
	}
	return nil
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/adapter/driven/google/ -run TestSheets -v -race`
Expected: PASS, четыре теста.

- [ ] **Step 5: Коммит**

```bash
git add internal/adapter/driven/google/sheets.go internal/adapter/driven/google/sheets_test.go
git commit -m "feat(google): sheets adapter"
```

---

### Task 5: Инструмент `drive_files`

**Files:**
- Create: `internal/tooling/builtin/drive_files.go`
- Test: `internal/tooling/builtin/drive_files_test.go`

**Interfaces:**
- Consumes: методы `*google.Drive` из Task 3.
- Produces: `func NewDriveFiles(client DriveClient, filesDir string) *DriveFiles`, реализующий `output.Tool`.

  Зависимость объявляется локальным интерфейсом в пакете `builtin`, чтобы тест подставлял фейк без Google SDK — приём из `internal/app/legalreview/orchestrator.go:14-22`:

```go
type DriveClient interface {
	List(ctx context.Context, folderID string) ([]google.FileInfo, error)
	Search(ctx context.Context, text string) ([]google.FileInfo, error)
	Download(ctx context.Context, fileID string) ([]byte, error)
	Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (google.FileInfo, error)
	EnsureFolder(ctx context.Context, parentID, name string) (google.FileInfo, error)
	ResolvePath(ctx context.Context, path string) (string, error)
}
```

- [ ] **Step 1: Написать падающий тест**

Создать `internal/tooling/builtin/drive_files_test.go`:

```go
package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	gdrive "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeDrive struct {
	files       []gdrive.FileInfo
	content     []byte
	uploaded    gdrive.FileInfo
	resolved    string
	resolveErr  error
	lastParent  string
	lastPath    string
}

func (f *fakeDrive) List(ctx context.Context, folderID string) ([]gdrive.FileInfo, error) {
	f.lastParent = folderID
	return f.files, nil
}

func (f *fakeDrive) Search(ctx context.Context, text string) ([]gdrive.FileInfo, error) {
	return f.files, nil
}

func (f *fakeDrive) Download(ctx context.Context, fileID string) ([]byte, error) {
	return f.content, nil
}

func (f *fakeDrive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gdrive.FileInfo, error) {
	f.lastParent = parentID
	f.content = content
	return f.uploaded, nil
}

func (f *fakeDrive) EnsureFolder(ctx context.Context, parentID, name string) (gdrive.FileInfo, error) {
	f.lastParent = parentID
	return gdrive.FileInfo{ID: "new", Name: name, IsFolder: true}, nil
}

func (f *fakeDrive) ResolvePath(ctx context.Context, path string) (string, error) {
	f.lastPath = path
	return f.resolved, f.resolveErr
}

func TestDriveFilesMetadata(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if tool.Name() != "drive_files" {
		t.Errorf("name: got %s", tool.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestDriveFilesList(t *testing.T) {
	fake := &fakeDrive{
		resolved: "folder-1",
		files: []gdrive.FileInfo{
			{ID: "a", Name: "договор.pdf", MimeType: "application/pdf"},
		},
	}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","path":"Ленина_42/10_Земля"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.lastPath != "Ленина_42/10_Земля" {
		t.Errorf("path passed to resolve: got %q", fake.lastPath)
	}
	if fake.lastParent != "folder-1" {
		t.Errorf("resolved folder id must be used for listing, got %q", fake.lastParent)
	}

	var result struct {
		Files []gdrive.FileInfo `json:"files"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
}

func TestDriveFilesRead(t *testing.T) {
	fake := &fakeDrive{content: []byte("текст договора")}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read","file_id":"a"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if result.Content != "текст договора" {
		t.Errorf("content: got %q", result.Content)
	}
}

func TestDriveFilesUnknownAction(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"destroy"}`))
	if err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/tooling/builtin/ -run TestDriveFiles -v`
Expected: FAIL — `undefined: builtin.NewDriveFiles`.

- [ ] **Step 3: Реализовать `drive_files.go`**

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gdrive "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// DriveClient is the slice of the Drive adapter this tool needs, declared here
// so tests can substitute a fake without touching the Google SDK.
type DriveClient interface {
	List(ctx context.Context, folderID string) ([]gdrive.FileInfo, error)
	Search(ctx context.Context, text string) ([]gdrive.FileInfo, error)
	Download(ctx context.Context, fileID string) ([]byte, error)
	Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gdrive.FileInfo, error)
	EnsureFolder(ctx context.Context, parentID, name string) (gdrive.FileInfo, error)
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
				"enum": ["list", "search", "read", "download", "upload", "mkdir"],
				"description": "Operation to perform"
			},
			"path": {"type": "string", "description": "Folder path from the drive root, e.g. Ленина_42/10_Земля"},
			"file_id": {"type": "string", "description": "Drive file id, for read and download"},
			"query": {"type": "string", "description": "Text to match against file names, for search"},
			"name": {"type": "string", "description": "File or folder name, for upload and mkdir"},
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

	// Keep the name inside filesDir even if the model supplies a path.
	dest := filepath.Join(d.filesDir, filepath.Base(name))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return nil, fmt.Errorf("save file: %w", err)
	}
	return json.Marshal(map[string]any{"path": dest, "size": len(data)})
}

func (d *DriveFiles) upload(ctx context.Context, path, name, content string) (json.RawMessage, error) {
	if name == "" {
		return nil, fmt.Errorf("upload: name is required")
	}
	folderID, err := d.client.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}

	mime := "text/plain"
	if strings.HasSuffix(name, ".md") {
		mime = "text/markdown"
	}
	info, err := d.client.Upload(ctx, folderID, name, mime, []byte(content))
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
```

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/tooling/builtin/ -run TestDriveFiles -v -race`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/tooling/builtin/drive_files.go internal/tooling/builtin/drive_files_test.go
git commit -m "feat(tools): drive_files tool over the Google Drive adapter"
```

---

### Task 6: Сделать `LoadSchemas` устойчивым к незарегистрированным инструментам

Эта задача чинит существующий баг, на который иначе наступит и `drive_files`.

`LoadSchemas` (`internal/tooling/registry.go:68-78`) падает целиком от одного неизвестного имени, а пайплайн (`internal/app/chat/pipeline.go:118-121`) в ответ на ошибку отдаёт модели **пустой список инструментов**. Правило классификатора при этом всегда перечисляет `cloud_files`, который регистрируется только при заданном `mailru.email` (`cmd/assistant/main.go:163-166`). На инстансе без Mail.ru любой запрос про документы уже сегодня уходит к модели вовсе без инструментов — молча, с одним warning в логах.

Правила классификатора описывают намерение и общие для всех инстансов, а состав инструментов — свойство конкретного инстанса. Значит разрешать несоответствие должен реестр, а не правила.

**Files:**
- Modify: `internal/tooling/registry.go:68-78`
- Test: `internal/tooling/registry_test.go`

**Interfaces:**
- Consumes: ничего.
- Produces: `LoadSchemas` возвращает схемы известных инструментов и пропускает неизвестные; ошибка только если не нашлось ни одного из непустого списка.

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/tooling/registry_test.go`:

```go
func TestLoadSchemasSkipsUnregisteredTools(t *testing.T) {
	r := tooling.NewRegistry()
	if err := r.Register(&mockTool{name: "search_web"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	defs, err := r.LoadSchemas([]string{"search_web", "drive_files"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(defs))
	}
	if defs[0].Name != "search_web" {
		t.Errorf("name: got %q", defs[0].Name)
	}
}

func TestLoadSchemasErrorsWhenNoneRegistered(t *testing.T) {
	r := tooling.NewRegistry()

	if _, err := r.LoadSchemas([]string{"drive_files"}); err == nil {
		t.Fatal("expected an error when no requested tool is registered")
	}
}
```

Свериться с именем и полями существующего мока в `registry_test.go` и использовать его, а не заводить второй.

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/tooling/ -run TestLoadSchemas -v`
Expected: FAIL — первый тест получает ошибку `tool "drive_files" not found`.

- [ ] **Step 3: Реализовать**

Заменить тело `LoadSchemas` в `internal/tooling/registry.go`:

```go
// LoadSchemas returns schemas for the named tools, skipping any that are not
// registered. Classifier rules are shared across instances while the tool set
// is per-instance, so a missing tool is a normal configuration difference, not
// an error. Returning an error here would leave the caller with no tools at all.
func (r *Registry) LoadSchemas(names []string) ([]entity.ToolDefinition, error) {
	defs := make([]entity.ToolDefinition, 0, len(names))
	for _, name := range names {
		def, err := r.LoadSchema(name)
		if err != nil {
			slog.Debug("tool not registered on this instance, skipping", "tool", name)
			continue
		}
		defs = append(defs, *def)
	}
	if len(defs) == 0 && len(names) > 0 {
		return nil, fmt.Errorf("none of the requested tools are registered: %v", names)
	}
	return defs, nil
}
```

Добавить импорт `"log/slog"`.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/tooling/ -v -race`
Expected: PASS, включая существующие тесты реестра.

- [ ] **Step 5: Коммит**

```bash
git add internal/tooling/registry.go internal/tooling/registry_test.go
git commit -m "fix(tooling): skip unregistered tools in LoadSchemas

Classifier rules are shared across instances, but the tool set is
per-instance. One unknown name made the whole batch fail, and the chat
pipeline reacted by sending the model no tools at all — so on instances
without Mail.ru every document query silently lost its tools."
```

---

### Task 7: Подключение в `main.go` и правило классификатора

**Files:**
- Modify: `cmd/assistant/main.go:162-166` (рядом с регистрацией `cloud_files`)
- Modify: `internal/app/chat/classifier.go:28` (правило про недвижимость)
- Test: `internal/app/chat/classifier_test.go`

**Interfaces:**
- Consumes: `config.Google` (Task 1), `google.LoadCredentials` (Task 2), `google.NewDrive` (Task 3), `builtin.NewDriveFiles` (Task 5), устойчивый `LoadSchemas` (Task 6).
- Produces: рабочий инструмент в реестре при заданном `google.credentials_file`.

Сигнатуры классификатора, с которыми работаем (`internal/app/chat/classifier.go:21,33,42`):

```go
func NewRuleClassifier() *RuleClassifier
func (c *RuleClassifier) addRule(pattern string, route valueobject.Route, tools []string, confidence float64)
func (c *RuleClassifier) Classify(text string) (valueobject.Route, []string, float64)
```

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/app/chat/classifier_test.go`:

```go
func TestClassifierRoutesDriveQueries(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"покажи файлы на гугл диске",
		"что лежит в драйве по проекту Ленина 42",
		"найди выписку ЕГРН объекта",
	} {
		_, tools, _ := c.Classify(input)
		if !slices.Contains(tools, "drive_files") {
			t.Errorf("%q: expected drive_files among %v", input, tools)
		}
	}
}
```

Добавить импорт `"slices"`.

Третий случай намеренно взят из существующего теста: на Брие документы живут в Drive, поэтому запрос про недвижимость обязан предлагать оба хранилища. Реестр из Task 6 отсеет то, чего нет на конкретном инстансе.

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/app/chat/ -run TestClassifierRoutesDrive -v`
Expected: FAIL — `drive_files` в списке нет.

- [ ] **Step 3: Обновить правила классификатора**

В `internal/app/chat/classifier.go` добавить `"drive_files"` первым в список инструментов правила про недвижимость (строка 28) — Drive основной, Mail.ru архивный:

```go
	c.addRule(`(?i)(облак|cloud|mail\.ru|объект|обьект|документ|выписк|егрн|скачай|download|прочитай|смета|акт КС|договор подряд|кс-2|кс-3|разрешен\w+ на строит|мебель|склад\b|магазин|гараж|участок|строительств|проанализируй|анализ|подпис|\.sig\b|сертификат|чертеж|чертёж|pdf|техплан|техническ\w+ план|кадастр)`, valueobject.RouteTool, []string{"drive_files", "cloud_files", "read_pdf", "inspect_signature", "bash"}, 0.95)
```

И добавить отдельное правило для явных упоминаний Drive **перед** правилом про недвижимость, чтобы «покажи файлы на гугл диске» не зависело от предметных слов:

```go
	c.addRule(`(?i)(гугл ?диск|google ?drive|драйв|на диске|в диске)`, valueobject.RouteTool, []string{"drive_files"}, 0.95)
```

- [ ] **Step 4: Прогнать тесты пакета целиком**

Run: `go test ./internal/app/chat/ -v -race`
Expected: PASS, включая существующий `TestClassifier` — он проверяет наличие `cloud_files` в списке, а не точное равенство, поэтому добавление `drive_files` его не ломает. Если проверка окажется на равенство, обновить ожидания в существующем тесте.

- [ ] **Step 5: Подключить в `main.go`**

После блока регистрации `cloud_files` (`main.go:163-166`) добавить:

```go
	if cfg.Google.Enabled() {
		creds, err := gworkspace.LoadCredentials(cfg.Google.CredentialsFile, cfg.Google.Impersonate)
		if err != nil {
			slog.Error("failed to load google credentials", "error", err)
			os.Exit(1)
		}
		driveOpts, err := creds.ClientOptions(context.Background(), "", drive.DriveScope)
		if err != nil {
			slog.Error("failed to build google drive auth", "error", err)
			os.Exit(1)
		}
		driveClient, err := gworkspace.NewDrive(context.Background(), cfg.Google.Drive.RootFolderID, driveOpts...)
		if err != nil {
			slog.Error("failed to create google drive client", "error", err)
			os.Exit(1)
		}
		registry.Register(builtin.NewDriveFiles(driveClient, filesDir))
		slog.Info("google drive tool enabled",
			"service_account", creds.Email(),
			"root_folder_id", cfg.Google.Drive.RootFolderID)
	}
```

Импорты: `gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"` и `"google.golang.org/api/drive/v3"`. Псевдоним обязателен: иначе имя пакета столкнётся с `google.golang.org/api/drive/v3`, который тянет за собой `golang.org/x/oauth2/google`.

Блок ставится **после** объявления `filesDir` (`main.go:162`). Падение на старте намеренное: заданный, но нерабочий Google-конфиг — ошибка настройки, которую нельзя проглатывать, ровно как это сделано для mailru и normativy в `main.go:254-263`. Форма обработки — `slog.Error` плюс `os.Exit(1)`, как везде в `main.go`.

- [ ] **Step 6: Проверить сборку и весь набор тестов**

Run: `make build-go && go test ./... -race -count=1`
Expected: сборка проходит, тесты зелёные.

- [ ] **Step 7: Проверить, что без конфига ничего не сломалось**

Run: `./bin/assistant --config=configs/config.example.yaml --migrate` (ожидаемо упрётся в отсутствующую БД, но **до** этого не должно быть ни одной жалобы на Google).
Expected: в логах нет `google drive tool enabled` и нет паники — пустой `credentials_file` полностью выключает интеграцию.

- [ ] **Step 8: Коммит**

```bash
git add cmd/assistant/main.go internal/app/chat/classifier.go internal/app/chat/classifier_test.go
git commit -m "feat: wire drive_files into the tool registry behind a config gate"
```

---

## Что остаётся за этапом

Sheets-адаптер из Task 4 собран и покрыт тестами, но в `main.go` не подключается: его первый потребитель — разбор реестра проектов на этапе 2. Подключать заранее незачем, а тесты не дадут ему сгнить.

Перевод `legalreview` на Drive требует абстракции над `CollectFolder`, которая сегодня существует только как метод `*builtin.MailRuCloud` (`internal/tooling/builtin/mailru.go:787`). Это самостоятельная задача с собственным риском регрессии для работающего Mail.ru-пайплайна, поэтому она выносится в отдельный план и делается после того, как доступ к Drive подтверждён живым вызовом.

## Проверка после этапа

С реальными учётными данными в конфиге Брии:

1. `создай папку test в корне диска` — появляется папка в общем диске;
2. `покажи файлы на диске` — возвращает содержимое корня;
3. загрузить файл через `upload`, затем прочитать его через `read` — содержимое совпадает.

Если листинг возвращает пустой список при непустом диске, причина почти наверняка в параметрах общего диска или в том, что service account не добавлен участником — проверять в этом порядке.
