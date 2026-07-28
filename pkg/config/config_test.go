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
	if cfg.Google.Gmail.ProcessedLabel != "Обработано" {
		t.Errorf("processed_label default: got %q", cfg.Google.Gmail.ProcessedLabel)
	}
	if cfg.Google.Gmail.IngestQuery != "in:inbox -label:Обработано" {
		t.Errorf("ingest_query default: got %q", cfg.Google.Gmail.IngestQuery)
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
