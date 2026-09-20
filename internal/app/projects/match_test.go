package projects_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

func registry() []projects.Project {
	return []projects.Project{
		{Name: "Ленина_42", Address: "г. Ногинск, ул. Ленина, д. 42"},
		{Name: "Vertex", Address: "Раменское, Северное шоссе, 8"},
		{Name: "Солощук_11", Address: "Солощук, участок 11"},
	}
}

func TestMatchProject(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		body    string
		from    string
		want    string
	}{
		{"по имени проекта", "КП по Ленина_42", "", "p@example.com", "Ленина_42"},
		{"по адресу в теме", "Приостановка", "Объект: ул. Ленина, д.42, Ногинск", "", "Ленина_42"},
		{"регистр и ё не мешают", "смета по объекту ВЕРТЕКС", "", "", ""},
		{"по слову в теле", "Документы", "направляем по объекту Vertex акты", "", "Vertex"},
		{"по отправителю", "Документы", "", "vertex-stroy@example.com", "Vertex"},
		{"нет совпадений", "Счёт на оплату", "реквизиты во вложении", "buh@example.com", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, candidates := projects.MatchProject(tc.subject, tc.body, tc.from, registry())
			if got.Name != tc.want {
				t.Errorf("project: got %q, want %q (candidates %v)", got.Name, tc.want, candidates)
			}
		})
	}
}

// Two projects in one letter is the case where guessing costs the most: the
// document lands in the wrong project's folder and is never looked for again.
func TestMatchProjectReportsConflict(t *testing.T) {
	got, candidates := projects.MatchProject(
		"Сверка по Ленина_42 и Vertex", "оба объекта", "", registry())

	if got.Name != "" {
		t.Errorf("ambiguous match must not resolve, got %q", got.Name)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected both candidates, got %v", candidates)
	}
}

// Words of an address scattered across a long letter are a coincidence.
func TestMatchProjectRejectsScatteredWords(t *testing.T) {
	body := "ул. Ленина в городе Королёв; " +
		"прочие сведения; далее по тексту; смета на 42 позиции"

	got, _ := projects.MatchProject("Разное", body, "", registry())
	if got.Name != "" {
		t.Errorf("scattered words matched %q", got.Name)
	}
}

func TestMatchProjectIgnoresEmptyRegistry(t *testing.T) {
	got, candidates := projects.MatchProject("Ленина_42", "", "", nil)
	if got.Name != "" || candidates != nil {
		t.Errorf("empty registry must not match: %q %v", got.Name, candidates)
	}
}
