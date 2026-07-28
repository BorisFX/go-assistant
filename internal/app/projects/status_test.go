package projects_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

func TestCheckStageSplitsPresentAndMissing(t *testing.T) {
	stage := projects.Stage{
		Num:      3,
		Name:     "Техпланы",
		Expected: []string{"Технические планы", "XML-файлы", "Заключения кадастрового инженера"},
	}

	st := projects.CheckStage(stage, []string{"Технический план корпус 1.pdf", "выгрузка.xml"})

	if len(st.Present) != 1 || st.Present[0] != "Технические планы" {
		t.Errorf("present: got %v", st.Present)
	}
	if len(st.Missing) != 2 {
		t.Errorf("missing: got %v", st.Missing)
	}
	if len(st.Files) != 2 {
		t.Error("the raw file list must travel with the verdict as evidence")
	}
}

// Case and ё are how the same document is written by two different people.
func TestCheckStageIgnoresCaseAndYo(t *testing.T) {
	stage := projects.Stage{Num: 6, Name: "Учёт", Expected: []string{"Решения о постановке на кадастровый учёт"}}

	st := projects.CheckStage(stage, []string{"РЕШЕНИЕ О ПОСТАНОВКЕ НА КАДАСТРОВЫЙ УЧЕТ.pdf"})
	if len(st.Missing) != 0 {
		t.Errorf("should have matched despite case and ё: %v", st.Missing)
	}
}

// Numbering people add to file names must not defeat the match.
func TestCheckStageIgnoresNumbering(t *testing.T) {
	stage := projects.Stage{Num: 2, Name: "Обследование", Expected: []string{"Акт обследования"}}

	st := projects.CheckStage(stage, []string{"12. Акт обследования 2025-07.pdf"})
	if len(st.Missing) != 0 {
		t.Errorf("numbering must not matter: %v", st.Missing)
	}
}

// Short common roots must not glue unrelated documents together.
func TestCheckStageDoesNotMatchUnrelated(t *testing.T) {
	stage := projects.Stage{Num: 7, Name: "Регистрация", Expected: []string{"Выписки ЕГРН"}}

	st := projects.CheckStage(stage, []string{"Договор аренды.pdf", "РС 1.pdf"})
	if len(st.Missing) != 1 {
		t.Errorf("unrelated files must not count as the document: %v", st.Present)
	}
}

func TestStageWithNoFilesIsNotDone(t *testing.T) {
	stage := projects.Stage{Num: 1, Name: "Подготовка", Expected: []string{"Дорожная карта"}}

	if projects.CheckStage(stage, nil).Done() {
		t.Error("an empty folder cannot be a finished stage")
	}
}

func TestCurrentStageIsFirstUnfinished(t *testing.T) {
	statuses := []projects.StageStatus{
		projects.CheckStage(projects.Stage{Num: 1, Name: "Подготовка", Expected: []string{"Дорожная карта"}},
			[]string{"Дорожная карта.pdf"}),
		projects.CheckStage(projects.Stage{Num: 2, Name: "Обследование", Expected: []string{"Акт обследования"}}, nil),
		projects.CheckStage(projects.Stage{Num: 3, Name: "Техпланы", Expected: []string{"Технические планы"}}, nil),
	}

	if got := projects.CurrentStage(statuses); got.Num != 2 {
		t.Errorf("current stage: got %d (%s)", got.Num, got.Stage)
	}
}
