package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeRegistry struct {
	list  []projects.Contractor
	added projects.Contractor
	err   error
}

func (f *fakeRegistry) Contractors(ctx context.Context) ([]projects.Contractor, error) {
	return f.list, f.err
}

func (f *fakeRegistry) Groups(ctx context.Context) (map[string]int, error) {
	groups := map[string]int{}
	for _, c := range f.list {
		groups[c.Group]++
	}
	return groups, f.err
}

func (f *fakeRegistry) ByGroup(ctx context.Context, group string) ([]projects.Contractor, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []projects.Contractor
	for _, c := range f.list {
		if strings.Contains(strings.ToLower(c.Group), strings.ToLower(group)) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("группа не найдена")
	}
	return out, nil
}

func (f *fakeRegistry) AddContractor(ctx context.Context, c projects.Contractor) error {
	f.added = c
	return f.err
}

type fakeDraftWriter struct {
	drafts   []gworkspace.Outgoing
	existing []gworkspace.DraftInfo
	deleted  []string
	err      error
}

func (f *fakeDraftWriter) ListDrafts(ctx context.Context, limit int) ([]gworkspace.DraftInfo, error) {
	return f.existing, nil
}

func (f *fakeDraftWriter) DeleteDraft(ctx context.Context, draftID string) error {
	f.deleted = append(f.deleted, draftID)
	return nil
}

func (f *fakeDraftWriter) CreateDraft(ctx context.Context, out gworkspace.Outgoing) (gworkspace.DraftInfo, error) {
	if f.err != nil {
		return gworkspace.DraftInfo{}, f.err
	}
	f.drafts = append(f.drafts, out)
	return gworkspace.DraftInfo{ID: "r" + out.To[0], To: out.To[0], Subject: out.Subject}, nil
}

func geoRegistry() *fakeRegistry {
	return &fakeRegistry{list: []projects.Contractor{
		{Name: "Геокад", Group: "Геодезия, геология, экология", Email: "centrgc@gmail.com"},
		{Name: "Мосэкопроект", Group: "Геодезия, геология, экология", Email: "info@mosecoproekt.ru", Contact: "Анна"},
		{Name: "Проекты и решения", Group: "Геодезия, геология, экология"},
		{Name: "ОДД ПРОФ-ПРОЕКТ", Group: "ПОДД", Email: "oddprof@yandex.ru"},
	}}
}

func TestContractorsGroups(t *testing.T) {
	tool := builtin.NewContractors(geoRegistry(), &fakeDraftWriter{}, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"groups"}`))
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if !strings.Contains(string(out), "ПОДД") || !strings.Contains(string(out), `"count":3`) {
		t.Errorf("groups: %s", out)
	}
}

// Every contractor gets their own letter: a shared Cc would show competitors to
// each other and invite price collusion.
func TestContractorsRfqDraftsOneLetterPerContractor(t *testing.T) {
	registry := geoRegistry()
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(registry, mail, t.TempDir())

	var batch builtin.Rfq
	tool.SetOnRfq(func(r builtin.Rfq) { batch = r })

	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"геодезия","subject":"Запрос КП","body":"Здравствуйте, {{имя}}! Прошу цену."}`))
	if err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if len(mail.drafts) != 2 {
		t.Fatalf("expected 2 drafts, got %d", len(mail.drafts))
	}
	for _, d := range mail.drafts {
		if len(d.To) != 1 {
			t.Errorf("letter addressed to %v", d.To)
		}
		if len(d.Cc) != 0 {
			t.Errorf("contractors must never share a Cc: %v", d.Cc)
		}
	}
	if !strings.Contains(mail.drafts[1].Body, "Анна") {
		t.Errorf("placeholder not personalised: %q", mail.drafts[1].Body)
	}
	if strings.Contains(mail.drafts[0].Body, "{{") {
		t.Errorf("placeholder left in the letter: %q", mail.drafts[0].Body)
	}
	if len(batch.Drafts) != 2 || len(batch.Skipped) != 1 {
		t.Errorf("confirmation batch: %+v", batch)
	}
	if !strings.Contains(string(out), "НЕ отправлены") {
		t.Errorf("result must say nothing was sent: %s", out)
	}
}

// A contractor without an address must be named, not quietly dropped.
func TestContractorsRfqReportsContractorsWithoutEmail(t *testing.T) {
	tool := builtin.NewContractors(geoRegistry(), &fakeDraftWriter{}, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"геодезия","subject":"Тема","body":"текст"}`))
	if err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if !strings.Contains(string(out), "Проекты и решения") {
		t.Errorf("contractor without email not reported: %s", out)
	}
}

func TestContractorsRfqAttachesTechSpec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ТЗ.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, dir)

	params := `{"action":"rfq","group":"ПОДД","subject":"ТЗ","body":"во вложении","attachments":["` + path + `"]}`
	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if len(mail.drafts[0].Attachments) != 1 {
		t.Fatalf("attachments: %+v", mail.drafts[0].Attachments)
	}
}

func TestContractorsRfqValidates(t *testing.T) {
	tool := builtin.NewContractors(geoRegistry(), &fakeDraftWriter{}, t.TempDir())

	for _, params := range []string{
		`{"action":"rfq","group":"геодезия","body":"текст"}`,
		`{"action":"rfq","group":"геодезия","subject":"Тема"}`,
		`{"action":"rfq","group":"монтаж лифтов","subject":"Тема","body":"текст"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err == nil {
			t.Errorf("accepted a broken mailing: %s", params)
		}
	}
}

func TestContractorsAdd(t *testing.T) {
	registry := geoRegistry()
	tool := builtin.NewContractors(registry, &fakeDraftWriter{}, t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"add","name":"Новый","group":"ПОДД","email":"new@example.com"}`))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if registry.added.Name != "Новый" || registry.added.Group != "ПОДД" {
		t.Errorf("added: %+v", registry.added)
	}
}

func TestContractorsListByGroup(t *testing.T) {
	tool := builtin.NewContractors(geoRegistry(), &fakeDraftWriter{}, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","group":"ПОДД"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(string(out), "Геокад") {
		t.Errorf("group filter ignored: %s", out)
	}
}

func TestContractorsUnknownAction(t *testing.T) {
	tool := builtin.NewContractors(geoRegistry(), &fakeDraftWriter{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send"}`)); err == nil {
		t.Error("sending must not be reachable from this tool")
	}
}

// Gmail не умеет править сохранённый черновик, поэтому правка текста — это
// пересборка пачки. Без удаления прежней в ящике копятся дубли, а пользователь
// видит, что «черновики не обновились».
func TestContractorsRfqReplacesPreviousMailing(t *testing.T) {
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, t.TempDir())
	first := `{"action":"rfq","group":"геодезия","subject":"Запрос КП","body":"первый вариант"}`
	second := `{"action":"rfq","group":"геодезия","subject":"Запрос КП","body":"Коллеги, вот ссылка"}`

	if _, err := tool.Execute(context.Background(), json.RawMessage(first)); err != nil {
		t.Fatalf("первая рассылка: %v", err)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(second))
	if err != nil {
		t.Fatalf("правка: %v", err)
	}

	if len(mail.deleted) != 2 {
		t.Errorf("прежние черновики не убраны: %v", mail.deleted)
	}
	if !strings.Contains(string(out), `"replaced":2`) {
		t.Errorf("пересборка не отражена в ответе: %s", out)
	}
	if len(mail.drafts) != 4 {
		t.Errorf("должно быть 2+2 созданных черновика, получено %d", len(mail.drafts))
	}
	if !strings.Contains(mail.drafts[3].Body, "ссылка") {
		t.Errorf("новый текст не доехал: %q", mail.drafts[3].Body)
	}
}

// Первая рассылка ничего не удаляет.
func TestContractorsRfqFirstRunDeletesNothing(t *testing.T) {
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"ПОДД","subject":"Тема","body":"текст"}`)); err != nil {
		t.Fatalf("рассылка: %v", err)
	}
	if len(mail.deleted) != 0 {
		t.Errorf("удалено лишнее: %v", mail.deleted)
	}
}

// Письмо обещает ТЗ во вложении, а файла нет — рассылка должна не состояться.
// Предупреждения мало: подрядчик получит пустое письмо и просто не ответит.
func TestContractorsRfqRefusesPromisedButMissingAttachment(t *testing.T) {
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"ПОДД","subject":"Запрос КП","body":"ТЗ во вложении, просим цену"}`))
	if err == nil {
		t.Fatal("рассылка без обещанного файла прошла")
	}
	if !strings.Contains(err.Error(), "create_pdf") {
		t.Errorf("ошибка не подсказывает, как получить файл: %v", err)
	}
	if len(mail.drafts) != 0 {
		t.Errorf("черновики всё равно созданы: %d", len(mail.drafts))
	}
}

func TestContractorsRfqListsAttachedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ТЗ.pdf")
	if err := os.WriteFile(path, []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, dir)
	var card builtin.Rfq
	tool.SetOnRfq(func(r builtin.Rfq) { card = r })

	params := `{"action":"rfq","group":"ПОДД","subject":"ТЗ","body":"во вложении","attachments":["` + path + `"]}`
	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if len(card.Attachments) != 1 || card.Attachments[0] != "ТЗ.pdf" {
		t.Errorf("вложения в карточке: %v", card.Attachments)
	}
	if card.Warning != "" {
		t.Errorf("ложное предупреждение: %s", card.Warning)
	}
}

// Черновики от прошлых прогонов убираются по метке письма, а не по теме: тему
// модель переписывает при каждой правке, метка же неизменна. Письма, написанные
// человеком, метки не имеют и остаются на месте.
func TestContractorsRfqRemovesStaleDraftsByTag(t *testing.T) {
	mail := &fakeDraftWriter{existing: []gworkspace.DraftInfo{
		{ID: "старый1", Subject: "Запрос КП", Tag: builtin.RfqTagFor("ПОДД")},
		{ID: "старый2", Subject: "Тема переписана", Tag: builtin.RfqTagFor("подд")},
		{ID: "другая группа", Subject: "Запрос КП", Tag: builtin.RfqTagFor("геодезия")},
		{ID: "письмо юриста", Subject: "Запрос КП"},
	}}
	tool := builtin.NewContractors(geoRegistry(), mail, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"ПОДД","subject":"Запрос КП","body":"текст"}`)); err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if len(mail.deleted) != 2 {
		t.Fatalf("убрано %v, ожидались оба старых черновика", mail.deleted)
	}
	for _, id := range mail.deleted {
		if id == "письмо юриста" || id == "другая группа" {
			t.Errorf("удалено лишнее: %s", id)
		}
	}
}

// Каждое письмо рассылки помечается — иначе прежние черновики не найти после
// перезапуска бота.
func TestContractorsRfqTagsLetters(t *testing.T) {
	mail := &fakeDraftWriter{}
	tool := builtin.NewContractors(geoRegistry(), mail, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"rfq","group":"геодезия","subject":"Запрос КП","body":"просим цену"}`)); err != nil {
		t.Fatalf("rfq: %v", err)
	}
	for _, d := range mail.drafts {
		if d.Tag != builtin.RfqTagFor("геодезия") {
			t.Errorf("метка письма: %q", d.Tag)
		}
		// Только ASCII: заголовок с кириллицей приезжает обратно перекодированным,
		// и прежние черновики перестают находиться.
		for _, r := range d.Tag {
			if r > 127 {
				t.Fatalf("метка не ASCII: %q", d.Tag)
			}
		}
	}
}
