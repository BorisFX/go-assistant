package legalreview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
)

// estimateTokens — дешёвая пред-оценка токенов (~chars/4). OpenRouter отдаёт
// реальный usage только ПОСЛЕ ответа, поэтому бюджет контекста стережём этим.
func estimateTokens(s string) int { return len(s) / 4 }

// formatDigests рендерит выжимки в тело запроса координатора: заголовок с путём
// и провенансом извлечения на документ. Пустая выжимка помечается «не прочитан»,
// чтобы пропавший документ не убрал молча юр-вывод (дизайн: обработка ошибок).
func formatDigests(digests []Digest) string {
	var b strings.Builder
	b.WriteString("Ниже — выжимки по каждому документу пачки. Сверь их между собой и с нормативной базой из системного промпта, найди юридические ошибки и расхождения, составь отчёт по форматам из системного промпта. Каждую находку привязывай к «(файл X, стр. N)».\n\n")
	for _, d := range digests {
		fmt.Fprintf(&b, "===== ДОКУМЕНТ: %s", d.Path)
		if d.Method != "" {
			fmt.Fprintf(&b, " (извлечение: %s)", d.Method)
		}
		b.WriteString(" =====\n")
		text := strings.TrimSpace(d.Text)
		if text == "" {
			text = "[документ не прочитан — извлечение не дало текста; не делай по нему выводов, но укажи его как непрочитанный]"
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return b.String()
}

const coordinatorSystemPrompt = `Ты — ведущий юрист-координатор, проверяющий пачку строительной документации на юридические ошибки и расхождения. На вход ты получаешь СТРУКТУРНЫЕ ВЫЖИМКИ по каждому документу (с дословными цитатами и ссылками на страницы), подготовленные аналитиками. Твоя задача — сверить их между собой и с нормативной базой ниже, найти ошибки/расхождения и составить отчёт.

Правила:
1. Опирайся ТОЛЬКО на факты из выжимок и нормативную базу. Не выдумывай данные, которых нет в выжимках.
2. Каждую находку подкрепляй привязкой «(файл X, стр. N)» из выжимки.
3. Если документ помечен «не прочитан» — НЕ делай по нему выводов; явно перечисли непрочитанные документы и предупреди, что вывод по ним невозможен. Никогда не считай отсутствие данных подтверждением соответствия.
4. Где данные извлечены дешёвым vision (а не выделенным OCR) и юридически критична дословность — помечай вывод как «требует проверки оригинала».
5. Выбери уместный формат(ы) отчёта под вход:
   - АНАЛИЗ ЗАМЕЧАНИЯ (для замечаний Росреестра): замечание регистратора → данные техплана → данные ЕГРН/разрешения → расхождение → причина → что делать.
   - ЗАКЛЮЧЕНИЕ ПО ТЕХПЛАНУ: описание / параметры / замечания / рекомендации.
   - ДОРОЖНАЯ КАРТА объекта: чеклист стадий.
6. Пиши по-русски, по делу, без воды.
7. ФОРМАТ ДЛЯ TELEGRAM. Отчёт читают в Telegram, он НЕ рендерит Markdown-таблицы и звёздочки. Поэтому строго:
   - НИКОГДА не используй таблицы из вертикальных палок (строки вида «| ... | ... |») и разделители «|---|---|». Вместо «сводной таблицы замечаний» давай НУМЕРОВАННЫЙ СПИСОК: каждое замечание — отдельным блоком с новой строки.
   - НЕ используй **двойные звёздочки** и __подчёркивания__ для жирного/курсива — они выводятся сырыми. Для акцента пиши ЗАГЛАВНЫМИ или ставь эмодзи.
   - Критичность помечай эмодзи: 🔴 критично, 🟠 серьёзно, 🟡 умеренно, 🟢 незначительно, ⬛ нет данных.
   - Заголовки разделов — простой строкой ЗАГЛАВНЫМИ (можно с эмодзи), без «#» и без «**».
   - Каждое замечание оформляй блоком, например:
     «1. 🔴 Площадь здания: 11 915,4 (техплан) vs 11 939,6 (РнС)
        Документы: TextPart стр. 4–5; РнС стр. 5
        Вывод: расхождение требует устранения.»
   Цель — чтобы текст читался как обычное сообщение, без сырой Markdown-разметки.
8. ИСТОЧНИК НОРМ. Ссылайся на статью или пункт нормы ТОЛЬКО если его текст есть в разделе «ВЫДЕРЖКИ ИЗ НОРМАТИВНОЙ БАЗЫ» во входе или получен инструментом norm_search (lookup: doc_code + ref, например doc_code="ГрК РФ", ref="ст. 49 ч. 2"; search: запрос по смыслу; docs: что загружено). Если норма нужна для вывода, а в выдержках её нет — СНАЧАЛА вызови norm_search и процитируй полученный текст. Только если инструмент ничего не вернул — пиши «текст нормы не загружен в базу, требует проверки» и НЕ цитируй и не пересказывай норму по памяти. Оглавление нормативной базы ниже — только карта, а не текст для цитирования. НЕ ДЕЛАЙ ОТДЕЛЬНОГО РАЗДЕЛА С ТЕКСТАМИ НОРМ в начале отчёта: норма приводится внутри того замечания, к которому относится, одной цитатой по делу. Бюджет отчёта уходит на замечания, а не на пересказ кодекса. ЦИТИРУЙ УСЛОВИЕ ЦЕЛИКОМ: если у предельного параметра есть оговорка, для каких объектов или случаев он действует («... 2000 кв. м для складских объектов; для объектов иного назначения не устанавливается»), приводи её в той же фразе. Обрезанная цитата, из которой пропала оговорка, — это ложное замечание, а не краткость.
9. ПСЕВДОДОКУМЕНТЫ ВО ВХОДЕ. «ЗАДАЧА ОТ ПОЛЬЗОВАТЕЛЯ» — это вопрос, на который отчёт обязан ответить в первую очередь. «КОНТЕКСТ ПЕРЕПИСКИ» — последние сообщения чата перед запросом: из них бери, что именно случилось с объектом (пришла приостановка, отказ, требование), какие сроки и что пользователь уже пробовал; отвечай с учётом этого, но не считай слова из переписки фактами документов — факты только из выжимок. «АВТОСВЕРКА» — детерминированное сравнение извлечённых чисел (ТЭП, кадастровые номера, адреса), сделанное программой: считай его проверенным фактом, приводи его строки 🔴 как замечания, а при расхождении с твоим прочтением выжимок — укажи оба значения и пометь «требует проверки оригинала». «СОСТАВ ПАКЕТА» — полный перечень документов пачки и их статус, составленный программой: ЭТО ЕДИНСТВЕННЫЙ ИСТОЧНИК ТОГО, ЧТО ПРОЧИТАНО. Объявлять документ непрочитанным можно ТОЛЬКО если он помечен «НЕ прочитан» в этом перечне. Если документ помечен «прочитан», а его фактов в выжимках перед тобой нет, пиши «данные документа не вошли в сводку, вывод по нему не делался» — это НЕ то же самое, что «не прочитан», и путать их нельзя. НЕ ПРИПИСЫВАЙ АВТОСВЕРКЕ ТО, ЧЕГО В ЕЁ БЛОКЕ НЕТ: ссылаться на неё можно только дословно по её строкам. Если документ перечислен в «В АВТОСВЕРКЕ НЕ УЧАСТВУЮТ», его числа программой не сверены — бери их только из его выжимки и так и пиши.

10. НИ ОДНОГО ЧИСЛА ИЗ ГОЛОВЫ. Каждый кадастровый номер, площадь, объём и высота в отчёте должны стоять в выжимке, в блоке АВТОСВЕРКИ или в процитированной норме. Если значения нет ни там, ни там — не пиши его вообще; написать «в томе не указана площадь участка» правильно, придумать её — брак. Программа сверяет числа отчёта с источниками и выносит неподтверждённые в отдельный раздел.`

// coordinatorMaxTokens is the report budget. At 8192 a real batch spent it on
// verbatim norm quotes and stopped at the third finding out of nineteen.
const coordinatorMaxTokens = 32000

// reduceMaxTokens is the compressor's own budget. It must stay small: the
// reduce pass runs per group on the cheap model, and a 32k answer there takes
// longer than the HTTP timeout allows.
const reduceMaxTokens = 8192

// truncationNotice heads a report the model did not get to finish.
const truncationNotice = "⚠️ ЗАКЛЮЧЕНИЕ ОБРЕЗАНО ПО ЛИМИТУ ВЫВОДА: часть замечаний в текст не попала. Ниже — только то, что модель успела написать; пакет проверен НЕ полностью.\n\n"

// defaultCoordinatorBudget — фолбэк бюджета входа в токенах (~40% окна 200K),
// если вызвали с непозитивным бюджетом. Стережёт от случайного 0.
const defaultCoordinatorBudget = 80000

// normRetriever supplies citable normative text for a batch: the corpus is
// asked for the units the digests reference plus semantic neighbours.
type normRetriever interface {
	Excerpts(ctx context.Context, texts []string, limit int) (string, error)
}

// excerptsLimit is how many corpus chunks a review may pull in.
const excerptsLimit = 12

// excerptsHeader opens the only section the coordinator may cite norms from.
const excerptsHeader = "## ВЫДЕРЖКИ ИЗ НОРМАТИВНОЙ БАЗЫ (единственный допустимый источник цитат)\n"

// Coordinator сводит выжимки пачки в один отчёт. Единственный вызов премиум-
// модели; нормативная база инлайнится в системный промпт (стабильный кэш-префикс).
type Coordinator struct {
	runner         subagentRunner
	norms          normRetriever // optional: nil when no corpus is configured
	tools          []string      // tools the coordinator may call (norm_search); empty = single shot
	model          string        // премиум-модель координатора (Sonnet)
	reduceModel    string        // дешёвая модель для reduce-прохода при переполнении
	normativy      string        // нормативная база, инлайнится в системный промпт
	maxInputTokens int           // бюджет входа ≈40% окна
}

func NewCoordinator(runner subagentRunner, model, reduceModel, normativy string, maxInputTokens int) *Coordinator {
	if maxInputTokens <= 0 {
		maxInputTokens = defaultCoordinatorBudget
	}
	return &Coordinator{
		runner:         runner,
		model:          model,
		reduceModel:    reduceModel,
		normativy:      normativy,
		maxInputTokens: maxInputTokens,
	}
}

// SetNormRetriever attaches the normative corpus. Without it the coordinator
// still works, but rule 8 then forbids every citation, which is the honest
// outcome for an instance with no loaded texts.
func (c *Coordinator) SetNormRetriever(r normRetriever) { c.norms = r }

// SetTools lets the coordinator call tools during the review. With norm_search
// it fetches the exact text of any norm the pre-loaded excerpts missed instead
// of writing «текст нормы не загружен».
func (c *Coordinator) SetTools(names ...string) { c.tools = names }

// coordinatorMaxTurns bounds the tool loop: enough for a handful of lookups,
// the final turn is forced to be text by the runner.
const coordinatorMaxTurns = 16

// finalPushTask is asked when the tool loop ends without a report: the model
// spent every turn looking norms up and never wrote the conclusion. An hour of
// digesting must not be lost to that.
const finalPushTask = "\n\nБЮДЖЕТ ПОИСКА ПО НОРМАМ ИСЧЕРПАН. Больше ничего не ищи и не запрашивай. Напиши итоговое заключение ПРЯМО СЕЙЧАС по тем данным, которые уже приведены выше. Для норм, текст которых получить не удалось, пиши «текст нормы не загружен в базу, требует проверки» и не цитируй их по памяти."

// systemPrompt — стабильный кэш-префикс: инструкция координатора + нормативная
// база. Адаптер OpenRouter кэширует системный префикс (Anthropic prompt cache).
func (c *Coordinator) systemPrompt() string {
	if strings.TrimSpace(c.normativy) == "" {
		return coordinatorSystemPrompt
	}
	return coordinatorSystemPrompt + "\n\n## НОРМАТИВНАЯ БАЗА\n" + c.normativy
}

// Conclusion is the coordinator's report together with everything it read on
// top of the digests: the normative excerpts and whatever norm_search
// returned. The verifier needs that full corpus — a quantity quoted from a
// norm the model fetched mid-run is backed, even though no digest holds it.
type Conclusion struct {
	Report   string
	Evidence []string
}

// Review сводит выжимки в отчёт. Пустой вход или пустой ответ модели — ошибка.
func (c *Coordinator) Review(ctx context.Context, digests []Digest) (Conclusion, error) {
	if len(digests) == 0 {
		return Conclusion{}, fmt.Errorf("coordinator: no digests to review")
	}
	sys := c.systemPrompt()

	// Program-built blocks are small, authoritative and must reach the model
	// word for word: compressing them is how a run lost the key document and
	// then reported read files as unread.
	kept, compressible := splitProgramBlocks(digests)
	fitted, err := c.fitToBudget(ctx, sys, compressible)
	if err != nil {
		return Conclusion{}, err
	}
	fitted = append(kept, fitted...)

	body := formatDigests(fitted)
	if excerpts := c.excerpts(ctx, sys, fitted, body); excerpts != "" {
		body = excerptsHeader + excerpts + "\n\n" + body
	}
	// The body is the evidence the model starts from; tool results are added
	// as they arrive below.
	evidence := []string{body}
	var mu sync.Mutex
	var lastFinish string
	start := time.Now()
	slog.Info("legalreview coordinator start",
		"model", c.model,
		"digests_in", len(digests), "digests_fitted", len(fitted),
		"sys_tokens", estimateTokens(sys), "body_tokens", estimateTokens(body),
		"budget_tokens", c.maxInputTokens)

	cfg := subagent.Config{
		Model:        c.model,
		SystemPrompt: sys,
		MaxTurns:     1,
		Temperature:  0,
		MaxTokens:    coordinatorMaxTokens,
		OnFinishReason: func(reason string) {
			mu.Lock()
			defer mu.Unlock()
			lastFinish = reason
		},
	}
	if len(c.tools) > 0 {
		cfg.ToolNames = c.tools
		cfg.MaxTurns = coordinatorMaxTurns
		cfg.OnToolResult = func(_, result string) {
			mu.Lock()
			defer mu.Unlock()
			evidence = append(evidence, result)
		}
	}
	out, err := c.runner.Run(ctx, cfg, body)
	if len(c.tools) > 0 && (err != nil || strings.TrimSpace(out) == "") {
		// The tool loop ended without a conclusion. Ask once more with the
		// tools withdrawn, so the only thing left to produce is the report.
		slog.Warn("legalreview coordinator: no report after the tool loop, asking again without tools",
			"model", c.model, "error", err)
		retry := cfg
		retry.ToolNames = nil
		retry.MaxTurns = 1
		retry.OnToolResult = nil
		out, err = c.runner.Run(ctx, retry, body+finalPushTask)
	}
	if err != nil {
		return Conclusion{}, fmt.Errorf("coordinator review: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return Conclusion{}, fmt.Errorf("coordinator: empty report")
	}
	mu.Lock()
	defer mu.Unlock()
	if lastFinish == "length" {
		slog.Warn("legalreview coordinator: report cut off by the output cap",
			"model", c.model, "max_tokens", coordinatorMaxTokens, "report_chars", len(out))
		out = truncationNotice + out
	}
	slog.Info("legalreview coordinator done",
		"model", c.model, "report_chars", len(out),
		"evidence_parts", len(evidence), "ms", time.Since(start).Milliseconds())
	return Conclusion{Report: out, Evidence: evidence}, nil
}

// excerpts fetches the normative text the fitted digests may cite. Excerpts
// count toward the input budget; when they do not fit, they are cut — never
// the digests, which are the evidence. A corpus failure is logged, not fatal:
// a review without citations beats no review.
func (c *Coordinator) excerpts(ctx context.Context, sys string, fitted []Digest, body string) string {
	if c.norms == nil {
		return ""
	}
	texts := make([]string, 0, len(fitted))
	for _, d := range fitted {
		if t := strings.TrimSpace(d.Text); t != "" {
			texts = append(texts, t)
		}
	}
	out, err := c.norms.Excerpts(ctx, texts, excerptsLimit)
	if err != nil {
		slog.Warn("legalreview norms excerpts failed", "error", err)
		return ""
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	room := c.maxInputTokens - estimateTokens(sys) - estimateTokens(body) - estimateTokens(excerptsHeader)
	if room <= 0 {
		slog.Warn("legalreview norms excerpts dropped: no budget left", "chunks_chars", len(out))
		return ""
	}
	if estimateTokens(out) > room {
		cut := room * 4
		for cut > 0 && cut < len(out) && out[cut]&0xC0 == 0x80 {
			cut--
		}
		out = out[:cut] + "\n[выдержки обрезаны по бюджету контекста]"
		slog.Info("legalreview norms excerpts truncated", "kept_tokens", room)
	}
	slog.Info("legalreview norms excerpts", "chars", len(out))
	return out
}

const reduceSystemPrompt = `Ты сжимаешь группу выжимок документов в ОДНУ компактную выжимку для последующей юридической проверки. Сохрани ВСЕ юридически значимые факты с дословными цитатами и ссылками «(файл X, стр. N)». Не делай юридических выводов. Не теряй данные — только убирай дублирование и воду. Формат: сначала строка «ФАЙЛЫ В БЛОКЕ:» с перечнем всех файлов группы через точку с запятой, затем список фактов с цитатами и привязкой к файлу/странице. НИ ОДИН ФАЙЛ НЕ ДОЛЖЕН ПРОПАСТЬ: если по файлу значимых фактов нет, всё равно оставь строку «файл X — значимых фактов не выявлено».`

// fitToBudget ужимает выжимки под бюджет координатора. Пока вход (системный
// промпт + выжимки) превышает бюджет и есть что группировать — гоняем дешёвый
// reduce-проход «выжимка-выжимок». Гарантированно завершается: если проход не
// уменьшил список (одна выжимка крупнее бюджета), возвращаем как есть — Sonnet
// получит максимально сжатый вход, что лучше отказа.
func (c *Coordinator) fitToBudget(ctx context.Context, sys string, digests []Digest) ([]Digest, error) {
	base := estimateTokens(sys)
	for {
		if base+estimateTokens(formatDigests(digests)) <= c.maxInputTokens || len(digests) <= 1 {
			return digests, nil
		}
		slog.Info("legalreview reduce pass",
			"model", c.reduceModel, "digests_before", len(digests),
			"input_tokens", base+estimateTokens(formatDigests(digests)), "budget_tokens", c.maxInputTokens)
		reduced, err := c.reducePass(ctx, digests)
		if err != nil {
			return nil, err
		}
		slog.Info("legalreview reduce pass done", "digests_after", len(reduced))
		if len(reduced) >= len(digests) { // не сходится — не зацикливаемся
			return reduced, nil
		}
		digests = reduced
	}
}

// reducePass группирует выжимки по символьному бюджету и сжимает каждую группу
// из >1 элемента в одну выжимку дешёвой моделью. Группу из одного элемента не
// трогаем (нечего сливать).
func (c *Coordinator) reducePass(ctx context.Context, digests []Digest) ([]Digest, error) {
	// Символьный бюджет на группу ≈ (бюджет − системный префикс) * 4. Пол — 1,
	// чтобы при недостижимом бюджете каждая выжимка осталась своей группой.
	tokenBudget := c.maxInputTokens - estimateTokens(c.systemPrompt())
	charBudget := tokenBudget * 4
	if charBudget < 1 {
		charBudget = 1
	}
	groups := groupDigests(digests, charBudget)
	// Когда символьный бюджет ниже одной выжимки, упаковка оставляет одни
	// одиночки и обычный «слить группу» не уменьшит вход. В этом случае всё
	// равно гоняем дешёвый reduce по каждой одиночке, чтобы ужать её саму; иначе
	// reduce не имел бы смысла. Признак — каждая выжимка в своей группе.
	reduceSingles := len(groups) == len(digests) && len(digests) > 1
	out := make([]Digest, 0, len(groups))
	for _, g := range groups {
		if len(g) == 1 && !reduceSingles {
			out = append(out, g[0])
			continue
		}
		cfg := subagent.Config{
			Model:        c.reduceModel,
			SystemPrompt: reduceSystemPrompt,
			MaxTurns:     1,
			Temperature:  0,
			MaxTokens:    reduceMaxTokens,
		}
		text, err := c.runner.Run(ctx, cfg, formatDigests(g))
		if err != nil {
			return nil, fmt.Errorf("coordinator reduce: %w", err)
		}
		out = append(out, Digest{Path: groupLabel(g), Text: strings.TrimSpace(text)})
	}
	return out, nil
}

// groupDigests режет выжимки на группы, чьё суммарное тело не превышает maxChars.
// Одна выжимка крупнее бюджета получает свою группу (ниже документа не дробим).
func groupDigests(digests []Digest, maxChars int) [][]Digest {
	if len(digests) == 0 {
		return nil
	}
	var (
		groups [][]Digest
		cur    []Digest
		curLen int
	)
	for _, d := range digests {
		n := len(d.Text)
		if len(cur) > 0 && curLen+n > maxChars {
			groups = append(groups, cur)
			cur, curLen = nil, 0
		}
		cur = append(cur, d)
		curLen += n
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// groupLabel склеивает пути группы в один ярлык для сжатой выжимки.
func groupLabel(g []Digest) string {
	paths := make([]string, len(g))
	for i, d := range g {
		paths[i] = d.Path
	}
	return strings.Join(paths, ", ")
}

// splitProgramBlocks separates what the program wrote from what the documents
// said. The first group goes to the coordinator untouched; only the second is
// ever handed to the compressor.
func splitProgramBlocks(digests []Digest) (program, documents []Digest) {
	for _, d := range digests {
		switch d.Path {
		case ManifestDigestPath, CollisionsDigestPath, FocusDigestPath, ContextDigestPath:
			program = append(program, d)
		default:
			documents = append(documents, d)
		}
	}
	return program, documents
}
