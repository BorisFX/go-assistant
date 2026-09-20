package builtin

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

// maxRfqRecipients bounds one mailing. Beyond this it is no longer a tender but
// a broadcast, and a mistake in the text would reach everyone at once.
const maxRfqRecipients = 30

// namePlaceholder is replaced per letter, so one template greets each contractor
// by their own name.
const namePlaceholder = "{{имя}}"

type contractorRegistry interface {
	Contractors(ctx context.Context) ([]projects.Contractor, error)
	Groups(ctx context.Context) (map[string]int, error)
	ByGroup(ctx context.Context, group string) ([]projects.Contractor, error)
	AddContractor(ctx context.Context, c projects.Contractor) error
}

type draftWriter interface {
	CreateDraft(ctx context.Context, out gworkspace.Outgoing) (gworkspace.DraftInfo, error)
	DeleteDraft(ctx context.Context, draftID string) error
	ListDrafts(ctx context.Context, limit int) ([]gworkspace.DraftInfo, error)
}

// Rfq is one mailing prepared for confirmation: N drafts, one per contractor.
type Rfq struct {
	Group       string                 `json:"group"`
	Subject     string                 `json:"subject"`
	Drafts      []gworkspace.DraftInfo `json:"drafts"`
	Skipped     []string               `json:"skipped,omitempty"`
	Replaced    int                    `json:"replaced,omitempty"`
	Attachments []string               `json:"attachments,omitempty"`
	Warning     string                 `json:"warning,omitempty"`
}

// Contractors exposes the Подрядчики sheet and turns a group into a tender
// mailing. Each contractor gets a separate letter — never a shared Cc, so one
// bidder cannot see who else is bidding.
type Contractors struct {
	registry contractorRegistry
	mail     draftWriter
	filesDir string
	onRfq    func(Rfq)

	// Последняя рассылка по каждой группе. Правка текста — это всегда новые
	// черновики: Gmail не редактирует их на месте. Без памяти о прошлой пачке
	// каждая правка удваивала число писем в ящике, а пользователь видел, что
	// «черновики не обновились», и просил ещё раз.
	mu   sync.Mutex
	last map[string][]string
}

func NewContractors(registry contractorRegistry, mail draftWriter, filesDir string) *Contractors {
	return &Contractors{registry: registry, mail: mail, filesDir: filesDir, last: map[string][]string{}}
}

// SetOnRfq wires the confirmation card, like the gmail tool's SetOnDraft.
func (c *Contractors) SetOnRfq(fn func(Rfq)) { c.onRfq = fn }

func (c *Contractors) Name() string { return "contractors" }

func (c *Contractors) Description() string {
	return "Contractor registry by trade group: list groups and contractors, add one, and prepare a tender mailing (one separate draft per contractor, confirmed by the user before sending)"
}

func (c *Contractors) Category() string { return "files" }

func (c *Contractors) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["groups", "list", "add", "rfq"],
				"description": "groups lists trade groups with counts; list shows contractors (optionally of one group); add appends a contractor; rfq prepares one draft per contractor of a group"
			},
			"group": {"type": "string", "description": "Trade group, e.g. геодезия. One word is enough"},
			"subject": {"type": "string", "description": "Letter subject, for rfq"},
			"body": {"type": "string", "description": "Letter text, for rfq. Use {{имя}} where the contractor's name should appear"},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Paths of local files to attach to every letter, e.g. the downloaded ТЗ"},
			"name": {"type": "string", "description": "Contractor name, for add"},
			"email": {"type": "string", "description": "Contractor email, for add"},
			"phone": {"type": "string", "description": "Contractor phone, for add"},
			"contact": {"type": "string", "description": "Contact person, for add"},
			"note": {"type": "string", "description": "Free-form note, for add"}
		},
		"required": ["action"]
	}`)
}

type contractorsParams struct {
	Action      string   `json:"action"`
	Group       string   `json:"group"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Contact     string   `json:"contact"`
	Note        string   `json:"note"`
}

func (c *Contractors) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p contractorsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	switch p.Action {
	case "groups":
		return c.groups(ctx)
	case "list":
		return c.list(ctx, p.Group)
	case "add":
		return c.add(ctx, p)
	case "rfq":
		return c.rfq(ctx, p)
	default:
		return nil, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (c *Contractors) groups(ctx context.Context) (json.RawMessage, error) {
	groups, err := c.registry.Groups(ctx)
	if err != nil {
		return nil, err
	}
	type group struct {
		Group string `json:"group"`
		Count int    `json:"count"`
	}
	out := make([]group, 0, len(groups))
	for name, count := range groups {
		out = append(out, group{Group: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return json.Marshal(map[string]any{"groups": out})
}

func (c *Contractors) list(ctx context.Context, group string) (json.RawMessage, error) {
	var (
		list []projects.Contractor
		err  error
	)
	if group == "" {
		list, err = c.registry.Contractors(ctx)
	} else {
		list, err = c.registry.ByGroup(ctx, group)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"group": group, "contractors": list})
}

func (c *Contractors) add(ctx context.Context, p contractorsParams) (json.RawMessage, error) {
	contractor := projects.Contractor{
		Name: p.Name, Group: p.Group, Email: p.Email,
		Phone: p.Phone, Contact: p.Contact, Note: p.Note,
	}
	if err := c.registry.AddContractor(ctx, contractor); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"added": contractor})
}

// rfq prepares the mailing: one draft per contractor, nothing sent. The user
// confirms the whole batch in Telegram.
func (c *Contractors) rfq(ctx context.Context, p contractorsParams) (json.RawMessage, error) {
	if strings.TrimSpace(p.Subject) == "" || strings.TrimSpace(p.Body) == "" {
		return nil, fmt.Errorf("рассылка: нужны тема и текст письма")
	}
	list, err := c.registry.ByGroup(ctx, p.Group)
	if err != nil {
		return nil, err
	}
	addressable, skipped := projects.WithEmail(list)
	if len(addressable) == 0 {
		return nil, fmt.Errorf("в группе %q ни у кого нет почты", p.Group)
	}
	if len(addressable) > maxRfqRecipients {
		return nil, fmt.Errorf("в группе %q %d адресатов — больше лимита %d на одну рассылку",
			p.Group, len(addressable), maxRfqRecipients)
	}

	// Письмо, обещающее вложение и уходящее без него, — это сорванный тендер:
	// подрядчик не отвечает, а бот рапортует «разослано». Поэтому отказ, а не
	// предупреждение: файл должен существовать до рассылки.
	if len(p.Attachments) == 0 && promisesAttachment(p.Body) {
		return nil, fmt.Errorf("в тексте письма сказано про вложение, но файлов не передано; " +
			"сначала создайте документ (drive_files action=create_pdf) или скачайте его " +
			"(drive_files action=download), затем укажите его local_path в attachments")
	}

	var attachments []gworkspace.OutgoingAttachment
	for _, path := range p.Attachments {
		att, err := readAttachment(c.filesDir, path)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, att)
	}

	batch := Rfq{Group: p.Group, Subject: p.Subject, Skipped: skipped}
	for _, att := range attachments {
		batch.Attachments = append(batch.Attachments, att.Name)
	}

	for _, contractor := range addressable {
		info, err := c.mail.CreateDraft(ctx, gworkspace.Outgoing{
			Tag: rfqTag(p.Group),
			// One recipient per letter. Competing bidders must not see each other.
			To:          []string{contractor.Email},
			Subject:     p.Subject,
			Body:        strings.ReplaceAll(p.Body, namePlaceholder, greetingName(contractor)),
			Attachments: attachments,
		})
		if err != nil {
			return nil, fmt.Errorf("черновик для %s: %w", contractor.Name, err)
		}
		batch.Drafts = append(batch.Drafts, info)
	}

	// Старая версия рассылки убирается только после того, как новая собрана
	// целиком: упади создание на середине — пользователь остался бы без обеих.
	replaced := c.replacePrevious(ctx, p.Group, p.Subject, batch.Drafts)
	batch.Replaced = replaced

	if c.onRfq != nil {
		c.onRfq(batch)
	}
	status := "черновики созданы и НЕ отправлены; подтверждение ушло пользователю в Telegram"
	if replaced > 0 {
		status = fmt.Sprintf("черновики пересозданы (прежние %d удалены), НЕ отправлены; подтверждение ушло пользователю в Telegram", replaced)
	}
	return json.Marshal(map[string]any{
		"group":       p.Group,
		"drafts":      len(batch.Drafts),
		"replaced":    replaced,
		"no_email":    skipped,
		"status":      status,
		"attachments": batch.Attachments,
		"warning":     batch.Warning,
		"recipients":  recipientNames(addressable),
	})
}

func greetingName(c projects.Contractor) string {
	if c.Contact != "" {
		return c.Contact
	}
	return c.Name
}

func recipientNames(list []projects.Contractor) []string {
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	return names
}

// replacePrevious discards the previous mailing to the same group and remembers
// the new one. Returns how many stale drafts were removed.
func (c *Contractors) replacePrevious(ctx context.Context, group, subject string, drafts []gworkspace.DraftInfo) int {
	ids := make([]string, 0, len(drafts))
	for _, d := range drafts {
		ids = append(ids, d.ID)
	}

	c.mu.Lock()
	previous := c.last[group]
	c.last[group] = ids
	c.mu.Unlock()

	// Память живёт только до перезапуска, а черновики — в ящике. Тему модель
	// переписывает от правки к правке, поэтому опираемся на метку письма.
	previous = append(previous, c.staleByTag(ctx, rfqTag(group), ids, previous)...)

	removed := 0
	for _, id := range previous {
		if err := c.mail.DeleteDraft(ctx, id); err != nil {
			// Черновик мог быть уже отправлен или удалён вручную — это не повод
			// валить пересборку рассылки.
			slog.Warn("не удалось убрать прежний черновик", "draft_id", id, "error", err)
			continue
		}
		removed++
	}
	return removed
}

// rfqTag is the marker written into every letter of a mailing.
//
// Строго ASCII: заголовок письма с кириллицей возвращается из Gmail в другой
// кодировке («group=Ð³ÐµÐ¾...»), сравнение перестаёт совпадать, и прежняя пачка
// не убирается — в ящике оказывается 38 писем вместо 19. Хэш от названия группы
// сравнивается надёжно и не зависит от перекодировок.
func rfqTag(group string) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(group))))
	return "rfq-" + hex.EncodeToString(sum[:])[:12]
}

// staleByTag finds leftover drafts of the same mailing that this process does
// not remember — everything from before the last restart, regardless of how the
// subject was reworded since.
func (c *Contractors) staleByTag(ctx context.Context, tag string, fresh, known []string) []string {
	if tag == "" {
		return nil
	}
	all, err := c.mail.ListDrafts(ctx, 50)
	if err != nil {
		slog.Warn("не удалось перечитать черновики", "error", err)
		return nil
	}
	skip := make(map[string]bool, len(fresh)+len(known))
	for _, id := range append(append([]string{}, fresh...), known...) {
		skip[id] = true
	}

	var stale []string
	for _, d := range all {
		// Только письма этой же рассылки: черновики, написанные человеком,
		// метки не имеют и остаются нетронутыми.
		if d.Tag == tag && !skip[d.ID] {
			stale = append(stale, d.ID)
		}
	}
	return stale
}

// promisesAttachment reports whether the letter text refers to a file that
// should be attached.
func promisesAttachment(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"вложени", "прилага", "во влож", "прикрепл", "attached"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// RfqTagFor exposes the marker for tests and one-off maintenance.
func RfqTagFor(group string) string { return rfqTag(group) }
