package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/docgen"
	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
	"github.com/olegmatyakubov/go-assistant/pkg/config"
)

// headlessReviewTimeout matches the Telegram intent's budget.
const headlessReviewTimeout = 30 * time.Minute

// runHeadlessReview is the --review-dir entry: collect a local directory,
// run the pipeline, write the report. It is the eval harness's front door —
// a fixed set of documents in, a report out, no Telegram in between.
// Returns the process exit code.
func runHeadlessReview(orch *legalreview.Orchestrator, cfg *config.Config, normativy, dir, out string) int {
	paths, err := collectLocalDir(dir, legalreview.ReviewExtensions, cfg.LegalReview.MaxFiles)
	if err != nil {
		slog.Error("review-dir: collect failed", "dir", dir, "error", err)
		return 1
	}
	if out == "" {
		out = filepath.Join(dir, "Заключение.md")
	}

	ctx, cancel := context.WithTimeout(context.Background(), headlessReviewTimeout)
	defer cancel()

	started := time.Now()
	slog.Info("review-dir: start", "dir", dir, "files", len(paths))
	report, err := orch.Review(ctx, paths)
	if err != nil {
		slog.Error("review-dir: review failed", "error", err)
		return 1
	}

	run := legalreview.Run{
		Folder:        dir,
		Files:         legalreview.DescribeFiles(paths),
		Report:        report,
		NormativyHash: legalreview.HashText(normativy),
		StartedAt:     started,
		FinishedAt:    time.Now(),
	}
	run.Models.Digest = cfg.LegalReview.DigestModel
	run.Models.Coordinator = cfg.LegalReview.CoordinatorModel

	markdown := legalreview.BuildReportMarkdown(run)
	if err := os.WriteFile(out, []byte(markdown), 0o644); err != nil {
		slog.Error("review-dir: write report failed", "path", out, "error", err)
		return 1
	}

	pdfPath := strings.TrimSuffix(out, filepath.Ext(out)) + ".pdf"
	if pdf, err := docgen.MarkdownToPDF(ctx, "Заключение", markdown); err != nil {
		// The markdown is already on disk; a missing LibreOffice on a dev box
		// must not fail the run.
		slog.Warn("review-dir: pdf not built", "error", err)
		pdfPath = ""
	} else if err := os.WriteFile(pdfPath, pdf, 0o644); err != nil {
		slog.Warn("review-dir: pdf not written", "path", pdfPath, "error", err)
		pdfPath = ""
	}

	fmt.Printf("review done: files=%d report_chars=%d elapsed=%s md=%s pdf=%s\n",
		len(paths), len(report), time.Since(started).Round(time.Second), out, orNone(pdfPath))
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// collectLocalDir walks dir recursively and keeps reviewable documents,
// guarded by the same file-count limit as the storage collectors.
func collectLocalDir(dir string, exts []string, maxFiles int) ([]string, error) {
	allow := make(map[string]bool, len(exts))
	for _, e := range exts {
		allow[strings.ToLower(e)] = true
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !allow[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("в %s нет документов с расширениями %s", dir, strings.Join(exts, ", "))
	}
	if maxFiles > 0 && len(paths) > maxFiles {
		return nil, fmt.Errorf("%d файлов превышает лимит %d", len(paths), maxFiles)
	}
	return paths, nil
}
