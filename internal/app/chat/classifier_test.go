package chat_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
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
		{"привет", nil, 0.5},  // simple greeting, no tools needed
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
