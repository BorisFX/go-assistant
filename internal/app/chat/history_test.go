package chat

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func TestBuildLLMMessagesReplaysToolTrace(t *testing.T) {
	history := []*entity.Message{
		entity.NewMessage(uuid.Nil, entity.RoleUser, "разбери СПОЗУ"),
		assistantWithCalls(entity.ToolCall{
			Name:   "read_pdf",
			Args:   `{"path":"/f/СПОЗУ.pdf","first_page":9}`,
			Result: "Проектом предусмотрено 22 м/м. Два м/м (10%) для МГН.",
		}),
		entity.NewMessage(uuid.Nil, entity.RoleUser, "сколько мест для МГН?"),
	}

	msgs := buildLLMMessages("system prompt", history)

	if msgs[0].Role != entity.RoleSystem || msgs[0].Content != "system prompt" {
		t.Fatalf("system prompt must come first, got %+v", msgs[0])
	}
	var traceIdx = -1
	for i, m := range msgs {
		if m.Role == entity.RoleSystem && strings.Contains(m.Content, "Два м/м") {
			traceIdx = i
		}
	}
	if traceIdx == -1 {
		t.Fatalf("tool trace never reached the request: %+v", msgs)
	}
	if msgs[traceIdx-1].Role != entity.RoleAssistant {
		t.Errorf("trace must follow its own assistant turn, got %q before it", msgs[traceIdx-1].Role)
	}
	if last := msgs[len(msgs)-1]; last.Content != "сколько мест для МГН?" {
		t.Errorf("the new question must stay last, got %q", last.Content)
	}
}

func TestBuildLLMMessagesWithoutTrace(t *testing.T) {
	history := []*entity.Message{
		entity.NewMessage(uuid.Nil, entity.RoleUser, "привет"),
		entity.NewMessage(uuid.Nil, entity.RoleAssistant, "привет"),
	}
	msgs := buildLLMMessages("sp", history)
	want := []output.LLMMessage{
		{Role: entity.RoleSystem, Content: "sp"},
		{Role: entity.RoleUser, Content: "привет"},
		{Role: entity.RoleAssistant, Content: "привет"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("expected %d messages, got %d: %+v", len(want), len(msgs), msgs)
	}
	for i := range want {
		if msgs[i].Role != want[i].Role || msgs[i].Content != want[i].Content {
			t.Errorf("message %d = %+v, want %+v", i, msgs[i], want[i])
		}
	}
}
