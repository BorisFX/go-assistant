package projects

import (
	"strings"
	"testing"
)

func TestDeliveryFolderUsesProjectsPrefix(t *testing.T) {
	got := deliveryFolder("Проекты", "Vertex", "Смета", "2026-09-17T10:00:00Z")
	if got != "Проекты/Vertex/99_Переписка/2026-09-17_Смета" {
		t.Fatalf("deliveryFolder = %q", got)
	}
	if got := deliveryFolder("Проекты", "", "Смета", "2026-09-17T10:00:00Z"); !strings.HasPrefix(got, UnsortedFolder+"/") {
		t.Fatalf("unsorted mail stays at the root inbox, got %q", got)
	}
}
