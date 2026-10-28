package legalreview

import (
	"strings"
)

// ContextDigestPath labels the pseudo-document carrying the recent chat: what
// the user said about the object before asking for the review.
const ContextDigestPath = "КОНТЕКСТ ПЕРЕПИСКИ"

const (
	// contextMaxTurns is how much of the chat the coordinator sees. Enough to
	// cover "вчера пришла приостановка, вот файлы", not the whole history.
	contextMaxTurns = 12
	// contextTurnChars caps one turn: a pasted suspension order is quoted, a
	// pasted 40-page statement is not.
	contextTurnChars = 1500
)

// ChatTurn is one conversation message as the review sees it: who said it
// and what. Tool traffic is not conversation and is skipped.
type ChatTurn struct {
	Role string // "user" | "assistant"
	Text string
}

// RenderContext turns recent chat into a text block the coordinator reads
// before the documents. Newest turns win when the limit is exceeded; the
// output is chronological so it reads like the chat did.
func RenderContext(turns []ChatTurn) string {
	var kept []ChatTurn
	for _, t := range turns {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		switch t.Role {
		case "user", "assistant":
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	if len(kept) > contextMaxTurns {
		kept = kept[len(kept)-contextMaxTurns:]
	}

	var b strings.Builder
	b.WriteString("Последние сообщения переписки с пользователем (от старых к новым):\n")
	for _, t := range kept {
		who := "Пользователь"
		if t.Role == "assistant" {
			who = "Ассистент"
		}
		text := strings.TrimSpace(t.Text)
		if r := []rune(text); len(r) > contextTurnChars {
			text = string(r[:contextTurnChars]) + "…"
		}
		b.WriteString("— " + who + ": " + text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
