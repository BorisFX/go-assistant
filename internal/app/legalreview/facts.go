package legalreview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
)

// TEP holds the technical-economic parameters that every construction
// document states in its own way and that the registrar compares across them.
// Pointers distinguish "not stated" from zero.
type TEP struct {
	AreaFootprintM2 *float64 `json:"area_footprint_m2"`
	AreaTotalM2     *float64 `json:"area_total_m2"`
	AreaBuildingM2  *float64 `json:"area_building_m2"`
	Floors          *float64 `json:"floors"`
	FloorsCount     *float64 `json:"floors_count"`
	HeightM         *float64 `json:"height_m"`
	VolumeM3        *float64 `json:"volume_m3"`
	LandAreaM2      *float64 `json:"land_area_m2"`
}

// Facts is the structured skeleton of one document: what the Go-side
// cross-check compares, as opposed to the prose digest the coordinator reads.
type Facts struct {
	DocType          string   `json:"doc_type"`
	Object           string   `json:"object"`
	Address          string   `json:"address"`
	CadastralNumbers []string `json:"cadastral_numbers"`
	DocumentNumbers  []string `json:"document_numbers"`
	Dates            []string `json:"dates"`
	TEP              TEP      `json:"tep"`
	Signers          []string `json:"signers"`
	NormRefs         []string `json:"norm_refs"`
}

// Empty reports whether nothing comparable was extracted.
func (f Facts) Empty() bool {
	return f.DocType == "" && f.Object == "" && f.Address == "" &&
		len(f.CadastralNumbers) == 0 && len(f.DocumentNumbers) == 0 &&
		len(f.Dates) == 0 && len(f.Signers) == 0 && len(f.NormRefs) == 0 &&
		f.TEP == TEP{}
}

const factsSystemPrompt = `Ты извлекаешь СТРУКТУРНЫЕ ДАННЫЕ из дайджеста одного строительного документа. Отвечай ТОЛЬКО одним JSON-объектом без пояснений и без markdown:
{"doc_type":"техплан|разрешение на строительство|ГПЗУ|выписка ЕГРН|приостановка Росреестра|проектная документация|договор|смета|чертёж|прочее",
 "object":"", "address":"", "cadastral_numbers":[], "document_numbers":[], "dates":[],
 "tep":{"area_footprint_m2":null,"area_total_m2":null,"area_building_m2":null,"floors":null,"floors_count":null,"height_m":null,"volume_m3":null,"land_area_m2":null},
 "signers":[], "norm_refs":[]}
Правила: числа пиши числами (1450.5), без единиц; чего в документе нет — null или пустой список; ничего не выдумывай; area_footprint_m2 — площадь застройки, area_total_m2 — общая площадь здания, area_building_m2 — площадь здания по техплану/ЕГРН, floors — этажность (надземная), floors_count — количество этажей, land_area_m2 — площадь участка; norm_refs — упомянутые нормы («ст. 26 218-ФЗ», «п. 4.3 СП 4.13130.2013»).`

const factsMaxTokens = 2048

// FactsExtractor asks the cheap model for a JSON skeleton of a digest. Any
// failure yields empty Facts: the prose digest still reaches the coordinator,
// and the cross-check simply has one document fewer to compare.
type FactsExtractor struct {
	runner subagentRunner
	model  string
}

func NewFactsExtractor(runner subagentRunner, model string) *FactsExtractor {
	return &FactsExtractor{runner: runner, model: model}
}

// Extract returns the facts of one digest, or empty Facts with a logged reason.
func (e *FactsExtractor) Extract(ctx context.Context, d Digest) Facts {
	if e == nil || e.runner == nil || strings.TrimSpace(d.Text) == "" {
		return Facts{}
	}
	cfg := subagent.Config{
		Model:        e.model,
		SystemPrompt: factsSystemPrompt,
		MaxTurns:     1,
		Temperature:  0,
		MaxTokens:    factsMaxTokens,
	}
	start := time.Now()
	out, err := e.runner.Run(ctx, cfg, fmt.Sprintf("Документ: %s\n\n%s", d.Path, d.Text))
	if err != nil {
		slog.Warn("legalreview facts: model call failed", "path", d.Path, "error", err)
		return Facts{}
	}
	facts, err := ParseFacts(out)
	if err != nil {
		slog.Warn("legalreview facts: unparsable output", "path", d.Path, "error", err)
		return Facts{}
	}
	slog.Info("legalreview facts", "path", d.Path, "doc_type", facts.DocType,
		"cadastral", len(facts.CadastralNumbers), "ms", time.Since(start).Milliseconds())
	return facts
}

// ParseFacts reads the model's JSON, tolerating code fences, prose around the
// object and Russian number formatting ("1 450,5") inside string values.
func ParseFacts(raw string) (Facts, error) {
	body := extractJSONObject(raw)
	if body == "" {
		return Facts{}, fmt.Errorf("no JSON object in output")
	}
	var loose struct {
		DocType          string         `json:"doc_type"`
		Object           string         `json:"object"`
		Address          string         `json:"address"`
		CadastralNumbers []string       `json:"cadastral_numbers"`
		DocumentNumbers  []string       `json:"document_numbers"`
		Dates            []string       `json:"dates"`
		TEP              map[string]any `json:"tep"`
		Signers          []string       `json:"signers"`
		NormRefs         []string       `json:"norm_refs"`
	}
	if err := json.Unmarshal([]byte(body), &loose); err != nil {
		return Facts{}, fmt.Errorf("decode: %w", err)
	}
	f := Facts{
		DocType:          strings.TrimSpace(loose.DocType),
		Object:           strings.TrimSpace(loose.Object),
		Address:          strings.TrimSpace(loose.Address),
		CadastralNumbers: cleanStrings(loose.CadastralNumbers),
		DocumentNumbers:  cleanStrings(loose.DocumentNumbers),
		Dates:            cleanStrings(loose.Dates),
		Signers:          cleanStrings(loose.Signers),
		NormRefs:         cleanStrings(loose.NormRefs),
	}
	f.TEP = TEP{
		AreaFootprintM2: numberOf(loose.TEP["area_footprint_m2"]),
		AreaTotalM2:     numberOf(loose.TEP["area_total_m2"]),
		AreaBuildingM2:  numberOf(loose.TEP["area_building_m2"]),
		Floors:          numberOf(loose.TEP["floors"]),
		FloorsCount:     numberOf(loose.TEP["floors_count"]),
		HeightM:         numberOf(loose.TEP["height_m"]),
		VolumeM3:        numberOf(loose.TEP["volume_m3"]),
		LandAreaM2:      numberOf(loose.TEP["land_area_m2"]),
	}
	return f, nil
}

// extractJSONObject returns the first balanced {...} in raw, skipping fences.
func extractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		switch {
		case inString:
			if c == '\\' {
				i++
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}

// numberOf accepts a JSON number, or a string like "1 450,5 м²", or null.
func numberOf(v any) *float64 {
	switch x := v.(type) {
	case float64:
		return &x
	case string:
		s := strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(x)
		// Drop a trailing unit ("м2", "м²", "м", "эт.") after the number.
		end := 0
		for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || s[end] == '-') {
			end++
		}
		if end == 0 {
			return nil
		}
		n, err := strconv.ParseFloat(s[:end], 64)
		if err != nil {
			return nil
		}
		return &n
	default:
		return nil
	}
}

func cleanStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
