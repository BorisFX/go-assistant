package legalreview

import (
	"context"
	"fmt"
	"strings"

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
6. Пиши по-русски, по делу, без воды.`

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

	cfg := subagent.Config{
		Model:        c.model,
		SystemPrompt: sys,
		MaxTurns:     1,
		Temperature:  0,
		MaxTokens:    coordinatorMaxTokens,
	}
	out, err := c.runner.Run(ctx, cfg, formatDigests(fitted))
	if err != nil {
		return "", fmt.Errorf("coordinator review: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("coordinator: empty report")
	}
	return out, nil
}

// fitToBudget ужимает выжимки под бюджет координатора иерархическим reduce.
// Полная реализация — Задача 3; пока пропускаем вход без изменений.
func (c *Coordinator) fitToBudget(_ context.Context, _ string, digests []Digest) ([]Digest, error) {
	return digests, nil
}
