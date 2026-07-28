// Package google adapts the official Google Workspace SDKs to this codebase's
// driven-port style: credentials are loaded once at startup, then handed to
// each API client as options. Tests substitute the endpoint through that same
// seam, so no test ever talks to Google.
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
// account itself; Gmail must act on behalf of a real mailbox, so the subject is
// passed per call rather than baked in here.
type Credentials struct {
	keyJSON     []byte
	email       string
	impersonate string
}

// LoadCredentials reads and checks a service account JSON key. Validation
// happens at startup so a misconfigured deployment fails immediately instead of
// at the first API call, hours later, inside a tool invocation.
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
		return nil, fmt.Errorf("parse service account key %s: %w", path, err)
	}
	if meta.Type != "service_account" {
		return nil, fmt.Errorf("service account key %s: expected type service_account, got %q", path, meta.Type)
	}
	if meta.ClientEmail == "" {
		return nil, fmt.Errorf("service account key %s: client_email is empty", path)
	}

	return &Credentials{keyJSON: data, email: meta.ClientEmail, impersonate: impersonate}, nil
}

// Email is the service account address that must be a member of the shared drive.
func (c *Credentials) Email() string { return c.email }

// Impersonate is the mailbox Gmail calls act as. Empty for Drive and Sheets.
func (c *Credentials) Impersonate() string { return c.impersonate }

// ClientOptions builds API client options for the given scopes. A non-empty
// subject issues tokens on behalf of that user, which requires domain-wide
// delegation to be configured for these scopes in the Workspace admin console.
func (c *Credentials) ClientOptions(ctx context.Context, subject string, scopes ...string) ([]option.ClientOption, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("client options: at least one scope is required")
	}
	cfg, err := google.JWTConfigFromJSON(c.keyJSON, scopes...)
	if err != nil {
		return nil, fmt.Errorf("build jwt config: %w", err)
	}
	cfg.Subject = subject
	return []option.ClientOption{option.WithTokenSource(cfg.TokenSource(ctx))}, nil
}
