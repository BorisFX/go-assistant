package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/app/norms"
)

// normCorpus is the slice of the norms service the tool needs.
type normCorpus interface {
	Lookup(ctx context.Context, docCode, ref string) ([]norms.Chunk, error)
	Search(ctx context.Context, query string, k int) ([]norms.Chunk, error)
	Documents(ctx context.Context) ([]norms.Document, error)
}

// NormSearch gives the model the text of the law instead of its memory of it.
type NormSearch struct {
	corpus normCorpus
}

func NewNormSearch(corpus normCorpus) *NormSearch { return &NormSearch{corpus: corpus} }

func (n *NormSearch) Name() string { return "norm_search" }

func (n *NormSearch) Description() string {
	return "Нормативная база (тексты законов, постановлений, СП/ГОСТ по редакциям). " +
		"lookup — точный текст пункта по документу и ссылке (doc_code='218-ФЗ', ref='ст. 26 ч. 1 п. 7'; doc_code='СП 4.13130', ref='п. 4.3'); " +
		"search — поиск нормы по смыслу; docs — какие документы загружены и в какой редакции. " +
		"ПРАВИЛО: ссылаться на статью или пункт нормы можно ТОЛЬКО по тексту, который вернул этот инструмент. " +
		"Если инструмент ничего не нашёл — так и пиши: «текст нормы не загружен в базу, требует проверки», и не цитируй по памяти."
}

func (n *NormSearch) Category() string { return "documents" }

func (n *NormSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["lookup", "search", "docs"],
				"description": "lookup: точный пункт по doc_code+ref; search: по смыслу (query); docs: список загруженных документов"
			},
			"doc_code": {"type": "string", "description": "Код документа для lookup: 218-ФЗ, ГрК РФ, ПП 87, СП 4.13130 (год можно опустить)"},
			"ref": {"type": "string", "description": "Ссылка на единицу для lookup: 'ст. 26 ч. 1 п. 7', 'ст. 51 ч. 7', 'п. 4.3', 'Приложение А'. Пусто — начало документа (область применения)"},
			"query": {"type": "string", "description": "Запрос по смыслу для search, например 'противопожарные расстояния между производственными зданиями'"},
			"limit": {"type": "integer", "description": "Сколько фрагментов вернуть для search, по умолчанию 5"}
		},
		"required": ["action"]
	}`)
}

type normSearchParams struct {
	Action  string `json:"action"`
	DocCode string `json:"doc_code"`
	Ref     string `json:"ref"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
}

type normHit struct {
	Document string `json:"document"`
	Ref      string `json:"ref"`
	Edition  string `json:"edition,omitempty"`
	Text     string `json:"text"`
}

const notLoadedNote = "текст нормы не загружен в базу — не цитируй его по памяти, отметь как требующий проверки"

func (n *NormSearch) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p normSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	switch p.Action {
	case "lookup":
		if strings.TrimSpace(p.DocCode) == "" {
			return nil, fmt.Errorf("lookup: нужен doc_code")
		}
		chunks, err := n.corpus.Lookup(ctx, p.DocCode, p.Ref)
		if err != nil {
			return nil, err
		}
		return n.render(ctx, chunks, fmt.Sprintf("%s %s", p.DocCode, p.Ref))
	case "search":
		if strings.TrimSpace(p.Query) == "" {
			return nil, fmt.Errorf("search: нужен query")
		}
		chunks, err := n.corpus.Search(ctx, p.Query, p.Limit)
		if err != nil {
			return nil, err
		}
		return n.render(ctx, chunks, p.Query)
	case "docs":
		docs, err := n.corpus.Documents(ctx)
		if err != nil {
			return nil, err
		}
		type doc struct {
			Code    string `json:"code"`
			Title   string `json:"title,omitempty"`
			Edition string `json:"edition,omitempty"`
		}
		out := make([]doc, 0, len(docs))
		for _, d := range docs {
			out = append(out, doc{Code: d.Code, Title: d.Title, Edition: d.Edition})
		}
		return json.Marshal(map[string]any{"documents": out, "count": len(out)})
	default:
		return nil, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (n *NormSearch) render(ctx context.Context, chunks []norms.Chunk, what string) (json.RawMessage, error) {
	if len(chunks) == 0 {
		return json.Marshal(map[string]any{
			"query": what, "hits": []normHit{}, "found": false, "note": notLoadedNote,
		})
	}
	editions := map[string]string{}
	if docs, err := n.corpus.Documents(ctx); err == nil {
		for _, d := range docs {
			editions[d.Code] = d.Edition
		}
	}
	hits := make([]normHit, 0, len(chunks))
	for _, c := range chunks {
		hits = append(hits, normHit{Document: c.DocCode, Ref: c.Ref, Edition: editions[c.DocCode], Text: c.Text})
	}
	return json.Marshal(map[string]any{
		"query": what, "hits": hits, "found": true,
		"instruction": "Цитируй только этот текст, с указанием документа, пункта и редакции.",
	})
}
