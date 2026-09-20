package norms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestFile describes the documents of a corpus folder. Without it a file
// is a document coded by its own name.
const manifestFile = "manifest.yaml"

// ManifestEntry is one document's metadata in manifest.yaml.
type ManifestEntry struct {
	Code    string `yaml:"code"`
	Title   string `yaml:"title"`
	Edition string `yaml:"edition"`
	Source  string `yaml:"source"`
}

// Source is a corpus file with its metadata, hash and extracted text.
type Source struct {
	Document Document
	Path     string
	Text     string
}

// LoadCorpus reads every supported file of dir. Unreadable files are skipped
// with a warning rather than failing the whole corpus: one broken PDF must
// not take the law out of the assistant.
func LoadCorpus(ctx context.Context, dir string) ([]Source, error) {
	manifest, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("norms: read %s: %w", dir, err)
	}
	var out []Source
	for _, e := range entries {
		if e.IsDir() || e.Name() == manifestFile || !supported(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("norms: skip unreadable file", "path", path, "error", err)
			continue
		}
		sum := sha256.Sum256(data)
		meta := manifest[e.Name()]
		if meta.Code == "" {
			meta.Code = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		text, err := extractText(ctx, path, data)
		if err != nil {
			slog.Warn("norms: skip file, text extraction failed", "path", path, "error", err)
			continue
		}
		out = append(out, Source{
			Document: Document{
				Code:     meta.Code,
				Title:    meta.Title,
				Edition:  meta.Edition,
				Source:   meta.Source,
				FileHash: hex.EncodeToString(sum[:]),
			},
			Path: path,
			Text: text,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Document.Code < out[j].Document.Code })
	return out, nil
}

func readManifest(dir string) (map[string]ManifestEntry, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if os.IsNotExist(err) {
		return map[string]ManifestEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("norms: read manifest: %w", err)
	}
	var m map[string]ManifestEntry
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("norms: parse manifest: %w", err)
	}
	if m == nil {
		m = map[string]ManifestEntry{}
	}
	return m, nil
}

func supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt", ".pdf", ".docx":
		return true
	}
	return false
}

// extractText returns the plain text of a corpus file. PDFs go through
// pdftotext, .docx through LibreOffice; both are external tools already used
// by the bot, so the corpus needs no extra dependencies.
func extractText(ctx context.Context, path string, data []byte) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt":
		return string(data), nil
	case ".pdf":
		return runPdftotext(ctx, path)
	case ".docx":
		return runSoffice(ctx, path)
	}
	return "", fmt.Errorf("unsupported extension")
}

func runPdftotext(ctx context.Context, path string) (string, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext not installed")
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", path, "-")
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func runSoffice(ctx context.Context, path string) (string, error) {
	bin, err := exec.LookPath("soffice")
	if err != nil {
		return "", fmt.Errorf("soffice not installed, cannot read .docx")
	}
	workdir, err := os.MkdirTemp("", "norms")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workdir)
	cmd := exec.CommandContext(ctx, bin, "--headless", "--norestore", "--invisible",
		"-env:UserInstallation=file://"+filepath.Join(workdir, "profile"),
		"--convert-to", "txt:Text (encoded):UTF8", "--outdir", workdir, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("soffice: %v: %s", err, strings.TrimSpace(string(out)))
	}
	txt := filepath.Join(workdir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".txt")
	data, err := os.ReadFile(txt)
	if err != nil {
		return "", fmt.Errorf("soffice output missing: %w", err)
	}
	return string(data), nil
}
