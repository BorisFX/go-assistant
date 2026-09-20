package config_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/pkg/config"
)

const legalReviewBase = `
legal_review:
  enabled: true
  normativy_path: /tmp/normativy.md
  digest_model: deepseek/deepseek-v4-flash
  coordinator_model: anthropic/claude-sonnet-4.6
  reduce_model: deepseek/deepseek-v4-flash
`

func TestLegalReviewNormsDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, legalReviewBase+"  norms_dir: /tmp/norms\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LegalReview.NormsDir != "/tmp/norms" {
		t.Errorf("norms_dir: got %q", cfg.LegalReview.NormsDir)
	}
	cfg, err = config.Load(writeConfig(t, legalReviewBase))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LegalReview.NormsDir != "" {
		t.Error("norms_dir must default to empty (no corpus)")
	}
}
