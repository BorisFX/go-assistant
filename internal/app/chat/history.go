package chat

import (
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// buildLLMMessages turns stored history into a request, replaying the recent
// tool results as data blocks after the turns that produced them. Without that
// replay a document read survives only as the model's prose about it, and the
// next question is answered from the retelling.
func buildLLMMessages(systemPrompt string, history []*entity.Message) []output.LLMMessage {
	traces := traceForHistory(history)

	msgs := make([]output.LLMMessage, 0, len(history)+len(traces)+1)
	msgs = append(msgs, output.LLMMessage{
		Role:    entity.RoleSystem,
		Content: systemPrompt,
	})

	for i, msg := range history {
		msgs = append(msgs, output.LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
		if block, ok := traces[i]; ok {
			msgs = append(msgs, output.LLMMessage{
				Role:    entity.RoleSystem,
				Content: block,
			})
		}
	}
	return msgs
}
