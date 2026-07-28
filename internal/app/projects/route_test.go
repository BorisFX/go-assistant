package projects_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

func routeRows() [][]string {
	return [][]string{
		{"Маршрут", "№", "Этап", "Ожидаемые документы", "Госорганы", "Норматив, дней"},
		{"ОНС_аренда", "1", "Подготовка", "Дорожная карта; Заключение по исходной документации", "Минимущество МО", "14"},
		{"ОНС_аренда", "3", "Техпланы", "Технические планы; XML-файлы", "", "30"},
		{"Ввод", "1", "Обследование", "Акт обследования", "", "10"},
	}
}

func TestParseRoutesGroupsByCode(t *testing.T) {
	routes := projects.ParseRoutes(routeRows())

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	ons := routes["ОНС_аренда"]
	if len(ons.Stages) != 2 {
		t.Fatalf("ОНС_аренда: expected 2 stages, got %d", len(ons.Stages))
	}
	if ons.Stages[0].Expected[1] != "Заключение по исходной документации" {
		t.Errorf("expected documents split wrong: %v", ons.Stages[0].Expected)
	}
	if ons.Stages[0].NormDays != 14 {
		t.Errorf("norm days: got %d", ons.Stages[0].NormDays)
	}
}

// A half-edited spreadsheet must not take the assistant down.
func TestParseRoutesSkipsBrokenRows(t *testing.T) {
	rows := append(routeRows(),
		[]string{"", "9", "Без кода", "x"},
		[]string{"ОНС_аренда", "не число", "Этап", "x"},
		[]string{"ОНС_аренда", "4"},
	)

	routes := projects.ParseRoutes(rows)
	if len(routes["ОНС_аренда"].Stages) != 2 {
		t.Errorf("broken rows must be skipped, got %d stages", len(routes["ОНС_аренда"].Stages))
	}
}

func TestStageFolderName(t *testing.T) {
	s := projects.Stage{Num: 3, Name: "Кадастровые работы"}
	if got := s.Folder(); got != "03_Кадастровые_работы" {
		t.Errorf("folder: got %q", got)
	}
}

func TestRouteFoldersWrapStagesWithSourceAndMail(t *testing.T) {
	r := projects.ParseRoutes(routeRows())["ОНС_аренда"]
	folders := r.Folders()

	if folders[0] != projects.SourceFolder {
		t.Errorf("first folder: got %q", folders[0])
	}
	if folders[len(folders)-1] != projects.MailFolder {
		t.Errorf("last folder: got %q", folders[len(folders)-1])
	}
	if len(folders) != 4 {
		t.Errorf("expected source + 2 stages + mail, got %v", folders)
	}
}
