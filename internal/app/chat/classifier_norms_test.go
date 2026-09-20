package chat_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
)

func TestClassifier_NormCitationsRouteToCorpus(t *testing.T) {
	c := chat.NewRuleClassifier()
	for _, in := range []string{
		"что говорит ст. 26 218-ФЗ о приостановке",
		"проверь п. 4.3 СП 4.13130",
		"какие нормы по отступам от границ",
		"состав разделов по постановлению правительства № 87",
		"СП 42.13330 пункт 7.1",
	} {
		_, tools, _ := c.Classify(in)
		found := false
		for _, tl := range tools {
			if tl == "norm_search" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: norm_search not offered, got %v", in, tools)
		}
	}
	_, tools, _ := c.Classify("привет, как дела")
	for _, tl := range tools {
		if tl == "norm_search" {
			t.Fatal("greeting must not route to the corpus")
		}
	}
}
