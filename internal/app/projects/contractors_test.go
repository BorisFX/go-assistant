package projects_test

import (
	"context"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

func contractorSvc() (*projects.Service, *fakeSheets) {
	sh := &fakeSheets{ranges: map[string][][]string{
		"Подрядчики": {
			{"Название", "Группа", "Email", "Телефон", "Сайт", "Контактное лицо", "Заметка"},
			{"Геокад", "Геодезия, геология, экология", "centrgc@gmail.com", "8 926 587-16-79", "centrgc.ru", "", "Геод 205"},
			{"Мосэкопроект", "Геодезия, геология, экология", "info@mosecoproekt.ru", "7 499 136-41-45", "", "", ""},
			{"Проекты и решения", "Обследование для реконструкции", "", "7 910 448-37-38", "", "", "почты нет"},
			{"ОДД ПРОФ-ПРОЕКТ", "ПОДД", "oddprof@yandex.ru", "", "", "", ""},
			{"", "", "", "", "", "", ""},
		},
	}}
	return projects.NewService(sh, &fakeDriveSvc{folders: map[string][]string{}}), sh
}

func TestContractorsReadsSheet(t *testing.T) {
	svc, _ := contractorSvc()

	list, err := svc.Contractors(context.Background())
	if err != nil {
		t.Fatalf("contractors: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("expected 4 contractors, got %d", len(list))
	}
	if list[0].Name != "Геокад" || list[0].Email != "centrgc@gmail.com" {
		t.Errorf("first row: %+v", list[0])
	}
}

func TestGroupsCountsMembers(t *testing.T) {
	svc, _ := contractorSvc()

	groups, err := svc.Groups(context.Background())
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if groups["Геодезия, геология, экология"] != 2 || groups["ПОДД"] != 1 {
		t.Errorf("groups: %v", groups)
	}
}

// Nobody retypes "Геодезия, геология, экология" — a one-word ask must resolve.
func TestByGroupMatchesOnWords(t *testing.T) {
	svc, _ := contractorSvc()

	for _, query := range []string{"геодезия", "Геология", "экология", "геодезия геология"} {
		list, err := svc.ByGroup(context.Background(), query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(list) != 2 {
			t.Errorf("%q matched %d contractors", query, len(list))
		}
	}
}

func TestByGroupReportsUnknownGroup(t *testing.T) {
	svc, _ := contractorSvc()

	_, err := svc.ByGroup(context.Background(), "монтаж лифтов")
	if err == nil {
		t.Fatal("expected an error for an unknown group")
	}
	if !strings.Contains(err.Error(), "ПОДД") {
		t.Errorf("error must list the available groups: %v", err)
	}
}

// A contractor without an address must be reported, not silently dropped: that
// is how someone quietly falls out of a tender.
func TestWithEmailReportsSkipped(t *testing.T) {
	svc, _ := contractorSvc()
	all, err := svc.Contractors(context.Background())
	if err != nil {
		t.Fatalf("contractors: %v", err)
	}

	addressable, skipped := projects.WithEmail(all)
	if len(addressable) != 3 {
		t.Errorf("addressable: %d", len(addressable))
	}
	if len(skipped) != 1 || skipped[0] != "Проекты и решения" {
		t.Errorf("skipped: %v", skipped)
	}
}

func TestAddContractorAppendsRow(t *testing.T) {
	svc, sh := contractorSvc()

	err := svc.AddContractor(context.Background(), projects.Contractor{
		Name: "Новый подрядчик", Group: "ПОДД", Email: "new@example.com",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sh.appended[0] != "Новый подрядчик" || sh.appended[1] != "ПОДД" {
		t.Errorf("appended row: %v", sh.appended)
	}
}

func TestAddContractorRequiresGroup(t *testing.T) {
	svc, _ := contractorSvc()

	err := svc.AddContractor(context.Background(), projects.Contractor{Name: "Без группы"})
	if err == nil {
		t.Fatal("expected an error without a group")
	}
}
