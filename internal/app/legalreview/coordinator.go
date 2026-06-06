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

var _ = context.Background
var _ = subagent.Config{}
