package builtin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

// reportDrive records uploads and refuses to resolve paths it was not told about,
// so the fallback into «Отчёты» is observable.
type reportDrive struct {
	fakeDrive
	known    map[string]string
	uploaded []string
}

func (r *reportDrive) EnsurePath(_ context.Context, path string) (string, error) {
	if id, ok := r.known[path]; ok {
		return id, nil
	}
	return "", errors.New("нет такой папки")
}

func (r *reportDrive) Upload(_ context.Context, parentID, name, mime string, _ []byte) (gworkspace.FileInfo, error) {
	r.uploaded = append(r.uploaded, parentID+"/"+name+"/"+mime)
	return gworkspace.FileInfo{ID: "f1", Name: name}, nil
}

func TestUploadReportGoesIntoReviewedFolder(t *testing.T) {
	drive := &reportDrive{known: map[string]string{"Vertex/03_Техпланы": "folder-1"}}
	d := builtin.NewDriveFiles(drive, t.TempDir())

	path, err := d.UploadReport(context.Background(), "/Vertex/03_Техпланы/", "Заключение.pdf", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "Vertex/03_Техпланы/Заключение.pdf" {
		t.Errorf("путь отчёта: %q", path)
	}
	if len(drive.uploaded) != 1 || !strings.HasPrefix(drive.uploaded[0], "folder-1/Заключение.pdf/application/pdf") {
		t.Errorf("загрузка: %v", drive.uploaded)
	}
}

// A Mail.ru folder name resolves nowhere on Drive: the report still has to
// land somewhere findable.
func TestUploadReportFallsBackToReportsFolder(t *testing.T) {
	drive := &reportDrive{known: map[string]string{"Отчёты": "reports-id"}}
	d := builtin.NewDriveFiles(drive, t.TempDir())

	path, err := d.UploadReport(context.Background(), "НОГИНСК ОБЪЕКТЫ/СОЛОЩУК", "Заключение.pdf", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "Отчёты/Заключение.pdf" {
		t.Errorf("путь отчёта: %q", path)
	}
	if len(drive.uploaded) != 1 || !strings.HasPrefix(drive.uploaded[0], "reports-id/") {
		t.Errorf("загрузка: %v", drive.uploaded)
	}
}

func TestUploadReportFailsWhenNoFolderAtAll(t *testing.T) {
	d := builtin.NewDriveFiles(&reportDrive{known: map[string]string{}}, t.TempDir())
	if _, err := d.UploadReport(context.Background(), "x", "r.pdf", []byte("%PDF")); err == nil {
		t.Error("без папки отчёт некуда класть — ожидалась ошибка")
	}
}
