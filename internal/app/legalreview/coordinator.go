package legalreview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
   Цель — чтобы текст читался как обычное сообщение, без сырой Markdown-разметки.`

const coordinatorMaxTokens = 8192

// defaultCoordinatorBudget — фолбэк бюджета входа в токенах (~40% окна 200K),
// если вызвали с непозитивным бюджетом. Стережёт от случайного 0.
const defaultCoordinatorBudget = 80000

// Coordinator сводит выжимки пачки в один отчёт. Единственный вызов премиум-
// модели; нормативная база инлайнится в системный промпт (стабильный кэш-префикс).
type Coordinator struct {
	runner         subagentRunner
	model          string // премиум-модель координатора (Sonnet)
	reduceModel    string // дешёвая модель для reduce-прохода при переполнении
	normativy      string // нормативная база, инлайнится в системный промпт
	maxInputTokens int    // бюджет входа ≈40% окна
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

// systemPrompt — стабильный кэш-префикс: инструкция координатора + нормативная
// база. Адаптер OpenRouter кэширует системный префикс (Anthropic prompt cache).
func (c *Coordinator) systemPrompt() string {
	if strings.TrimSpace(c.normativy) == "" {
		return coordinatorSystemPrompt
	}
	return coordinatorSystemPrompt + "\n\n## НОРМАТИВНАЯ БАЗА\n" + c.normativy
}

// Review сводит выжимки в отчёт. Пустой вход или пустой ответ модели — ошибка.
func (c *Coordinator) Review(ctx context.Context, digests []Digest) (string, error) {
	if len(digests) == 0 {
		return "", fmt.Errorf("coordinator: no digests to review")
	}
	sys := c.systemPrompt()

	fitted, err := c.fitToBudget(ctx, sys, digests)
	if err != nil {
		return "", err
	}

	body := formatDigests(fitted)
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
	}
	out, err := c.runner.Run(ctx, cfg, body)
	if err != nil {
		return "", fmt.Errorf("coordinator review: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("coordinator: empty report")
	}
	slog.Info("legalreview coordinator done",
		"model", c.model, "report_chars", len(out), "ms", time.Since(start).Milliseconds())
	return out, nil
}

const reduceSystemPrompt = `Ты сжимаешь группу выжимок документов в ОДНУ компактную выжимку для последующей юридической проверки. Сохрани ВСЕ юридически значимые факты с дословными цитатами и ссылками «(файл X, стр. N)». Не делай юридических выводов. Не теряй данные — только убирай дублирование и воду. Формат: список фактов с цитатами и привязкой к файлу/странице.`

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
			MaxTokens:    coordinatorMaxTokens,
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
