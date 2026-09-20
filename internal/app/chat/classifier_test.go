package chat_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
)

func TestClassifier(t *testing.T) {
	c := chat.NewRuleClassifier()

	tests := []struct {
		input         string
		wantTools     []string
		minConfidence float64
	}{
		{"статус бота", []string{"trading_status"}, 0.9},
		{"что с ботом?", []string{"trading_status"}, 0.9},
		{"balance", []string{"trading_status"}, 0.9},
		{"загугли что такое DDD", []string{"search_web"}, 0.9},
		{"объект мебель 24 проанализируй разрешение", []string{"cloud_files"}, 0.9},
		{"найди выписку ЕГРН объекта", []string{"cloud_files"}, 0.9},
		{"покажи документы склада", []string{"cloud_files"}, 0.9},
		{"скачай смету", []string{"cloud_files"}, 0.9},
		{"перезапусти nginx", []string{"bash"}, 0.9},
		{"привет", nil, 0.5}, // simple greeting, no tools needed
		{"что думаешь о криптовалюте?", nil, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, tools, confidence := c.Classify(tt.input)

			if confidence < tt.minConfidence {
				t.Errorf("input %q: expected confidence >= %f, got %f", tt.input, tt.minConfidence, confidence)
			}

			if len(tt.wantTools) > 0 {
				found := false
				for _, want := range tt.wantTools {
					for _, got := range tools {
						if want == got {
							found = true
						}
					}
				}
				if !found {
					t.Errorf("input %q: expected tools %v in %v", tt.input, tt.wantTools, tools)
				}
			}
		})
	}
}

func TestClassifierRoutesDriveQueries(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"покажи файлы на гугл диске",
		"что лежит в драйве по проекту Ленина 42",
		"найди выписку ЕГРН объекта",
	} {
		_, tools, _ := c.Classify(input)

		found := false
		for _, got := range tools {
			if got == "drive_files" {
				found = true
			}
		}
		if !found {
			t.Errorf("input %q: expected drive_files in %v", input, tools)
		}
	}
}

// A request about a project must offer the projects tool. Without it the model
// cannot learn the route or the stage and is left to invent them.
func TestClassifierOffersProjectsTool(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"составь КП по проекту Vertex, 6 объектов",
		"на каком этапе проект Мира_7",
		"заведи проект в реестр",
		"какие маршруты есть",
	} {
		_, tools, _ := c.Classify(input)

		found := false
		for _, got := range tools {
			if got == "projects" {
				found = true
			}
		}
		if !found {
			t.Errorf("input %q: expected projects in %v", input, tools)
		}
	}
}

// Drawing requests share vocabulary with the document rules ("подпись", "документ"),
// so generate_image has to be offered explicitly or those rules crowd it out.
func TestClassifierOffersImageToolForDrawingRequests(t *testing.T) {
	c := chat.NewRuleClassifier()

	inputs := []string{
		"нарисуй красного кота на ноутбуке",
		"сгенерируй картинку с пальмами",
		"добавь ему на фото пиратскую шляпу",
		"отредактируй фотку, убери фон",
		"сделай мем из этой картинки",
		"draw a cat wearing sunglasses",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			_, tools, _ := c.Classify(input)

			for _, tool := range tools {
				if tool == "generate_image" {
					return
				}
			}
			t.Errorf("input %q classified to %v, want generate_image among them", input, tools)
		})
	}
}

// Mail questions must reach the gmail tool. Without this rule the mailbox is
// only available when some other keyword happens to match first.
func TestClassifierRoutesMailQueries(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"что пришло на почту сегодня",
		"найди письмо от подрядчика",
		"посмотри вложения по переписке",
	} {
		_, tools, _ := c.Classify(input)

		found := false
		for _, got := range tools {
			if got == "gmail" {
				found = true
			}
		}
		if !found {
			t.Errorf("input %q: expected gmail in %v", input, tools)
		}
	}
}

// A tender mailing is asked for by trade group, so the contractors tool must be
// offered on those words — without it the model invents addressees.
func TestClassifierRoutesContractorMailings(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"разошли ТЗ по геодезии подрядчикам",
		"покажи группы подрядчиков",
		"кому разослать запрос КП",
	} {
		_, tools, _ := c.Classify(input)

		found := false
		for _, got := range tools {
			if got == "contractors" {
				found = true
			}
		}
		if !found {
			t.Errorf("input %q: expected contractors in %v", input, tools)
		}
	}
}

// Moving an object's folder from Mail.ru into Drive needs both storages offered
// at once, or the model only sees the one it happened to match on.
func TestClassifierRoutesCloudToDriveMigration(t *testing.T) {
	c := chat.NewRuleClassifier()

	for _, input := range []string{
		"перенеси документы по объекту с мейла в гугл",
		"переложи папку Солощук на диск",
	} {
		_, tools, _ := c.Classify(input)

		var drive, cloud bool
		for _, got := range tools {
			switch got {
			case "drive_files":
				drive = true
			case "cloud_files":
				cloud = true
			}
		}
		if !drive || !cloud {
			t.Errorf("input %q: expected both storages in %v", input, tools)
		}
	}
}

// A follow-up in the middle of work carries no keywords of its own: "переделай
// на эту ссылку" after a mailing must not lose the mail tools, or the bot
// answers "инструмент почты недоступен" mid-task.
func TestClassifyWithContextKeepsToolsFromRecentTurns(t *testing.T) {
	c := chat.NewRuleClassifier()
	history := []string{
		"подготовь рассылку КП по подрядчикам",
		"вместо названия компании пиши «Коллеги»",
	}

	_, tools, _ := c.ClassifyWithContext("переделай на ссылку вместо гугла на эту https://cloud.mail.ru/public/dHhi/h2Sg1vzRe", history)

	var gmail, contractors bool
	for _, got := range tools {
		switch got {
		case "gmail":
			gmail = true
		case "contractors":
			contractors = true
		}
	}
	if !gmail || !contractors {
		t.Errorf("инструменты рассылки потеряны: %v", tools)
	}
}

// Context must not drag tools forever: an unrelated question after the mailing
// should not keep loading mail schemas.
func TestClassifyWithContextIsBounded(t *testing.T) {
	c := chat.NewRuleClassifier()
	history := []string{"подготовь рассылку КП по подрядчикам"}
	for i := 0; i < 7; i++ {
		history = append(history, "что там по объекту")
	}

	_, tools, _ := c.ClassifyWithContext("привет", history)

	for _, got := range tools {
		if got == "gmail" {
			t.Errorf("почта тянется из давнего сообщения: %v", tools)
		}
	}
}

// The route still comes from the current message: a greeting stays a chat turn
// even when the previous turns were tool work.
func TestClassifyWithContextKeepsCurrentRoute(t *testing.T) {
	c := chat.NewRuleClassifier()

	route, _, _ := c.ClassifyWithContext("привет", []string{"подготовь рассылку КП"})
	if route != valueobject.RouteChat {
		t.Errorf("маршрут взят из контекста: %v", route)
	}
}
