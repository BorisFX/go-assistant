package legalreview

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseFacts_PlainJSON(t *testing.T) {
	raw := `{"doc_type":"техплан","object":"Склад","address":"МО, г. Ногинск","cadastral_numbers":["50:16:0000000:1"],
	"document_numbers":[],"dates":["2026-03-18"],"tep":{"area_footprint_m2":2429.3,"area_total_m2":3317.6,"floors":1,"floors_count":null,"height_m":null,"volume_m3":27513,"land_area_m2":null},
	"signers":["Чаплыгин М.Н."],"norm_refs":["ст. 26 218-ФЗ"]}`
	f, err := ParseFacts(raw)
	if err != nil {
		t.Fatalf("ParseFacts: %v", err)
	}
	if f.DocType != "техплан" || f.Object != "Склад" || len(f.CadastralNumbers) != 1 {
		t.Fatalf("header wrong: %+v", f)
	}
	if f.TEP.AreaFootprintM2 == nil || *f.TEP.AreaFootprintM2 != 2429.3 || f.TEP.Floors == nil || *f.TEP.Floors != 1 {
		t.Fatalf("tep wrong: %+v", f.TEP)
	}
	if f.TEP.FloorsCount != nil || f.TEP.HeightM != nil {
		t.Fatalf("null must stay nil: %+v", f.TEP)
	}
}

func TestParseFacts_FencedWithProseAndRussianNumbers(t *testing.T) {
	raw := "Вот данные:\n```json\n{\"doc_type\":\"ГПЗУ\",\"tep\":{\"area_total_m2\":\"1 450,5 м²\",\"floors\":\"3 эт.\",\"land_area_m2\":\"12 000\"}}\n```\nГотово."
	f, err := ParseFacts(raw)
	if err != nil {
		t.Fatalf("ParseFacts: %v", err)
	}
	if f.TEP.AreaTotalM2 == nil || *f.TEP.AreaTotalM2 != 1450.5 {
		t.Fatalf("russian number not parsed: %+v", f.TEP.AreaTotalM2)
	}
	if f.TEP.Floors == nil || *f.TEP.Floors != 3 || f.TEP.LandAreaM2 == nil || *f.TEP.LandAreaM2 != 12000 {
		t.Fatalf("unit-suffixed numbers not parsed: %+v", f.TEP)
	}
}

func TestParseFacts_GarbageErrors(t *testing.T) {
	if _, err := ParseFacts("не могу извлечь"); err == nil {
		t.Fatalf("want error without JSON")
	}
	if _, err := ParseFacts("{\"doc_type\": "); err == nil {
		t.Fatalf("want error for truncated JSON")
	}
}

func TestFactsEmpty(t *testing.T) {
	if !(Facts{}).Empty() {
		t.Fatalf("zero Facts must be empty")
	}
	v := 1.0
	if (Facts{TEP: TEP{Floors: &v}}).Empty() {
		t.Fatalf("facts with a tep value are not empty")
	}
}

func TestFactsExtractor_UsesCheapModelAndNeverFails(t *testing.T) {
	runner := &fakeRunner{outputs: []string{`{"doc_type":"договор","document_numbers":["12/26"]}`}}
	e := NewFactsExtractor(runner, "deepseek/deepseek-v4-flash")
	f := e.Extract(context.Background(), Digest{Path: "/d/contract.pdf", Text: "Договор № 12/26"})
	if f.DocType != "договор" || len(f.DocumentNumbers) != 1 {
		t.Fatalf("facts not extracted: %+v", f)
	}
	if runner.cfgs[0].Model != "deepseek/deepseek-v4-flash" || runner.cfgs[0].Temperature != 0 || runner.cfgs[0].MaxTurns != 1 {
		t.Fatalf("unexpected subagent config: %+v", runner.cfgs[0])
	}
	if !strings.Contains(runner.tasks[0], "/d/contract.pdf") {
		t.Fatalf("task must name the document: %q", runner.tasks[0])
	}

	failing := NewFactsExtractor(&fakeRunner{err: errors.New("llm down")}, "m")
	if f := failing.Extract(context.Background(), Digest{Path: "/d/x.pdf", Text: "t"}); !f.Empty() {
		t.Fatalf("model failure must yield empty facts, got %+v", f)
	}
	garbage := NewFactsExtractor(&fakeRunner{outputs: []string{"???"}}, "m")
	if f := garbage.Extract(context.Background(), Digest{Path: "/d/x.pdf", Text: "t"}); !f.Empty() {
		t.Fatalf("unparsable output must yield empty facts, got %+v", f)
	}
	if f := e.Extract(context.Background(), Digest{Path: "/d/unread.pdf"}); !f.Empty() {
		t.Fatalf("unread document must not be sent to the model")
	}
}
