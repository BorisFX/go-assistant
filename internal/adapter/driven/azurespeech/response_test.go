package azurespeech

import "testing"

func TestDisplayText(t *testing.T) {
	text, err := displayText([]byte(`{"RecognitionStatus":"Success","DisplayText":"Привет, Дебилыч."}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "Привет, Дебилыч." {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestDisplayTextRejectsInvalidJSON(t *testing.T) {
	if _, err := displayText([]byte(`not-json`)); err == nil {
		t.Fatal("expected a parse error")
	}
}
