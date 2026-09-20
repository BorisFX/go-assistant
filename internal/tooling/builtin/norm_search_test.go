package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/norms"
)

type fakeCorpus struct {
	lookup []norms.Chunk
	search []norms.Chunk
	docs   []norms.Document
	gotDoc string
	gotRef string
	gotQ   string
}

func (f *fakeCorpus) Lookup(_ context.Context, doc, ref string) ([]norms.Chunk, error) {
	f.gotDoc, f.gotRef = doc, ref
	return f.lookup, nil
}

func (f *fakeCorpus) Search(_ context.Context, q string, _ int) ([]norms.Chunk, error) {
	f.gotQ = q
	return f.search, nil
}

func (f *fakeCorpus) Documents(context.Context) ([]norms.Document, error) { return f.docs, nil }

func TestNormSearch_LookupReturnsTextWithEdition(t *testing.T) {
	c := &fakeCorpus{
		lookup: []norms.Chunk{{DocCode: "СП 4.13130.2013", Ref: "п. 4.3", Text: "Противопожарные расстояния"}},
		docs:   []norms.Document{{Code: "СП 4.13130.2013", Edition: "2020"}},
	}
	tool := NewNormSearch(c)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"lookup","doc_code":"СП 4.13130","ref":"п. 4.3"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.gotDoc != "СП 4.13130" || c.gotRef != "п. 4.3" {
		t.Fatalf("params not passed: %q %q", c.gotDoc, c.gotRef)
	}
	var res struct {
		Found bool      `json:"found"`
		Hits  []normHit `json:"hits"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Found || len(res.Hits) != 1 || res.Hits[0].Edition != "2020" || res.Hits[0].Text != "Противопожарные расстояния" {
		t.Fatalf("result: %s", out)
	}
}

func TestNormSearch_NotFoundSaysNotLoaded(t *testing.T) {
	tool := NewNormSearch(&fakeCorpus{})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"lookup","doc_code":"СНиП 2.01","ref":"п. 1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"found":false`) || !strings.Contains(string(out), "не загружен") {
		t.Fatalf("expected not-loaded note: %s", out)
	}
}

func TestNormSearch_SearchAndDocs(t *testing.T) {
	c := &fakeCorpus{
		search: []norms.Chunk{{DocCode: "218-ФЗ", Ref: "ст. 26 ч. 1 п. 7", Text: "форма"}},
		docs:   []norms.Document{{Code: "218-ФЗ", Title: "О регистрации", Edition: "2024"}},
	}
	tool := NewNormSearch(c)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"форма документа"}`))
	if err != nil || c.gotQ != "форма документа" || !strings.Contains(string(out), "ст. 26 ч. 1 п. 7") {
		t.Fatalf("search: %v %s", err, out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"docs"}`))
	if err != nil || !strings.Contains(string(out), `"count":1`) || !strings.Contains(string(out), "О регистрации") {
		t.Fatalf("docs: %v %s", err, out)
	}
}

func TestNormSearch_Validation(t *testing.T) {
	tool := NewNormSearch(&fakeCorpus{})
	for _, in := range []string{`{"action":"lookup"}`, `{"action":"search"}`, `{"action":"nope"}`} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(in)); err == nil {
			t.Fatalf("expected error for %s", in)
		}
	}
	if tool.Name() != "norm_search" || !strings.Contains(tool.Description(), "ТОЛЬКО") {
		t.Fatal("tool identity or citation rule missing")
	}
}
