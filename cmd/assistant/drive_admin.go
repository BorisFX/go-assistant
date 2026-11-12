package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// driveAdmin is the slice of the Drive adapter the housekeeping modes need.
type driveAdmin interface {
	Root() string
	List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error)
	EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error)
	Move(ctx context.Context, fileID, newParentID string) (gworkspace.FileInfo, error)
}

// runDriveTree is the --drive-tree entry: print the shared drive as an
// indented tree with ids, so a reorganisation can be planned by hand.
func runDriveTree(d driveAdmin, depth int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fmt.Print(renderDriveTree(ctx, d, "", depth))
	return 0
}

func renderDriveTree(ctx context.Context, d driveAdmin, folderID string, depth int) string {
	var b strings.Builder
	walkDriveTree(ctx, d, folderID, 0, depth, &b)
	return b.String()
}

func walkDriveTree(ctx context.Context, d driveAdmin, folderID string, level, depth int, b *strings.Builder) {
	if level > depth {
		return
	}
	items, err := d.List(ctx, folderID)
	if err != nil {
		fmt.Fprintf(b, "%s!! list failed: %v\n", strings.Repeat("  ", level), err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsFolder != items[j].IsFolder {
			return items[i].IsFolder
		}
		return items[i].Name < items[j].Name
	})
	for _, it := range items {
		indent := strings.Repeat("  ", level)
		if it.IsFolder {
			fmt.Fprintf(b, "%s%s/ [%s]\n", indent, it.Name, it.ID)
			walkDriveTree(ctx, d, it.ID, level+1, depth, b)
			continue
		}
		fmt.Fprintf(b, "%s%s [%s] %s\n", indent, it.Name, it.ID, humanSize(it.Size))
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// runDriveMoves is the --drive-moves entry: apply a plan file of
// "<file id>\t<destination path>" lines. Destination folders are created as
// needed; "/" as the destination means the drive root, and "+" as the id
// only ensures the folder exists (scaffolding stages without moving anything).
// The plan is validated in full before the first move, so a typo cannot leave
// the drive half-reorganised.
func runDriveMoves(d driveAdmin, planPath string) int {
	plan, err := os.ReadFile(planPath)
	if err != nil {
		slog.Error("drive-moves: read plan", "path", planPath, "error", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	n, err := applyDriveMoves(ctx, d, string(plan))
	if err != nil {
		slog.Error("drive-moves: failed", "moved", n, "error", err)
		return 1
	}
	slog.Info("drive-moves: done", "moved", n)
	return 0
}

type driveMove struct{ id, dest string }

func applyDriveMoves(ctx context.Context, d driveAdmin, plan string) (int, error) {
	var moves []driveMove
	sc := bufio.NewScanner(strings.NewReader(plan))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		id, dest, ok := strings.Cut(sc.Text(), "\t")
		id, dest = strings.TrimSpace(id), strings.TrimSpace(dest)
		toRoot := dest == "/"
		dest = strings.Trim(dest, "/")
		if !ok || id == "" || (dest == "" && !toRoot) || (id == "+" && dest == "") {
			return 0, fmt.Errorf("plan line %d: want \"<file id><TAB><dest path>\", got %q", line, sc.Text())
		}
		moves = append(moves, driveMove{id: id, dest: dest})
	}

	folders := map[string]string{"": d.Root()}
	n := 0
	for _, m := range moves {
		parent, ok := folders[m.dest]
		if !ok {
			parent = ""
			for _, seg := range strings.Split(m.dest, "/") {
				f, err := d.EnsureFolder(ctx, parent, seg)
				if err != nil {
					return n, fmt.Errorf("ensure %q: %w", m.dest, err)
				}
				parent = f.ID
			}
			folders[m.dest] = parent
		}
		if m.id == "+" {
			slog.Info("drive-moves: ensured", "path", m.dest)
			continue
		}
		if _, err := d.Move(ctx, m.id, parent); err != nil {
			return n, fmt.Errorf("move %s -> %s: %w", m.id, m.dest, err)
		}
		n++
		slog.Info("drive-moves: moved", "id", m.id, "to", m.dest)
	}
	return n, nil
}
