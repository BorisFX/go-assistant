package legalreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// digestCache keeps one JSON file per digested document under dir. The key is
// the file's content hash plus the salt (models + prompts), so the same PDF
// under another name hits, and the same name with new bytes misses. A nil
// cache is a no-op, which keeps the orchestrator free of nil checks.
type digestCache struct {
	dir  string
	salt string
}

func newDigestCache(dir, salt string) *digestCache {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("legalreview digest cache disabled", "dir", dir, "error", err)
		return nil
	}
	return &digestCache{dir: dir, salt: salt}
}

func (c *digestCache) key(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	h.Write([]byte(c.salt))
	return hex.EncodeToString(h.Sum(nil)), true
}

// Get returns the cached digest for path, re-pointed at the current path so
// the report cites the file the user actually sent.
func (c *digestCache) Get(path string) (Digest, bool) {
	if c == nil {
		return Digest{}, false
	}
	k, ok := c.key(path)
	if !ok {
		return Digest{}, false
	}
	data, err := os.ReadFile(filepath.Join(c.dir, k+".json"))
	if err != nil {
		return Digest{}, false
	}
	var d Digest
	if err := json.Unmarshal(data, &d); err != nil || d.Text == "" {
		return Digest{}, false
	}
	d.Path = path
	return d, true
}

// Put stores a finished digest; failures are logged, never fatal.
func (c *digestCache) Put(path string, d Digest) {
	if c == nil || d.Text == "" {
		return
	}
	k, ok := c.key(path)
	if !ok {
		return
	}
	data, err := json.Marshal(d)
	if err != nil {
		return
	}
	tmp := filepath.Join(c.dir, k+".json.part")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Warn("legalreview digest cache write failed", "path", path, "error", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, k+".json")); err != nil {
		slog.Warn("legalreview digest cache write failed", "path", path, "error", err)
	}
}
