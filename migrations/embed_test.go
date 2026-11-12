package migrations_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/migrations"
)

func TestEmbeddedMigrationsPresent(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			data, err := fs.ReadFile(migrations.FS, e.Name())
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			if len(strings.TrimSpace(string(data))) == 0 {
				t.Errorf("migration %s is empty", e.Name())
			}
			sqlFiles = append(sqlFiles, e.Name())
		}
	}

	if len(sqlFiles) < 6 {
		t.Fatalf("expected at least 6 SQL migrations embedded, got %d (%v)", len(sqlFiles), sqlFiles)
	}
}
