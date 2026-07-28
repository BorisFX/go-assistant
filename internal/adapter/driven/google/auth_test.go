package google_test

import (
	"os"
	"path/filepath"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// A structurally valid service account key. The private key is not a real one:
// LoadCredentials checks the envelope, not the cryptography.
const testKeyJSON = `{
  "type": "service_account",
  "project_id": "test",
  "private_key_id": "abc",
  "private_key": "-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n",
  "client_email": "yuri-bot@test.iam.gserviceaccount.com",
  "client_id": "123",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func writeKey(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func TestLoadCredentialsMissingFile(t *testing.T) {
	if _, err := gworkspace.LoadCredentials(filepath.Join(t.TempDir(), "nope.json"), ""); err == nil {
		t.Fatal("expected an error for a missing key file")
	}
}

func TestLoadCredentialsMalformed(t *testing.T) {
	if _, err := gworkspace.LoadCredentials(writeKey(t, "not json"), ""); err == nil {
		t.Fatal("expected an error for a malformed key file")
	}
}

// A downloaded OAuth client secret looks like JSON but is not a service account
// key. Catching it here turns a confusing runtime 401 into a startup message.
func TestLoadCredentialsWrongKeyType(t *testing.T) {
	body := `{"type": "authorized_user", "client_email": "x@y.z"}`
	if _, err := gworkspace.LoadCredentials(writeKey(t, body), ""); err == nil {
		t.Fatal("expected an error for a non service_account key")
	}
}

func TestLoadCredentialsMissingClientEmail(t *testing.T) {
	body := `{"type": "service_account", "project_id": "test"}`
	if _, err := gworkspace.LoadCredentials(writeKey(t, body), ""); err == nil {
		t.Fatal("expected an error when client_email is empty")
	}
}

func TestLoadCredentialsValid(t *testing.T) {
	creds, err := gworkspace.LoadCredentials(writeKey(t, testKeyJSON), "info@samostrou.net")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if creds.Email() != "yuri-bot@test.iam.gserviceaccount.com" {
		t.Errorf("email: got %q", creds.Email())
	}
	if creds.Impersonate() != "info@samostrou.net" {
		t.Errorf("impersonate: got %q", creds.Impersonate())
	}
}
