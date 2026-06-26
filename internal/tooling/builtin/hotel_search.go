package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// HotelSearch finds hotels with prices via booking-com15 on RapidAPI.
type HotelSearch struct {
	client   *RapidAPIClient
	currency string
	limit    int
}

func NewHotelSearch(client *RapidAPIClient, currency string, limit int) *HotelSearch {
	if currency == "" {
		currency = "USD"
	}
	if limit <= 0 {
		limit = 6
	}
	return &HotelSearch{client: client, currency: currency, limit: limit}
}

func (h *HotelSearch) Name() string { return "hotel_search" }
func (h *HotelSearch) Description() string {
	return "Search hotels with live prices for a city and dates"
}
func (h *HotelSearch) Category() string { return "travel" }

func (h *HotelSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"location": {"type": "string", "description": "City name, e.g. Bangkok"},
			"check_in": {"type": "string", "description": "Check-in date, format YYYY-MM-DD"},
			"check_out": {"type": "string", "description": "Check-out date, format YYYY-MM-DD"},
			"adults": {"type": "integer", "description": "Number of adults", "default": 2},
			"budget_max": {"type": "number", "description": "Optional max price per night"},
			"min_score": {"type": "number", "description": "Optional minimum review score (0-10)"}
		},
		"required": ["location", "check_in", "check_out"]
	}`)
}

type hotelSearchParams struct {
	Location  string  `json:"location"`
	CheckIn   string  `json:"check_in"`
	CheckOut  string  `json:"check_out"`
	Adults    int     `json:"adults"`
	BudgetMax float64 `json:"budget_max"`
	MinScore  float64 `json:"min_score"`
}

type bcHotelDestResp struct {
	Data []struct {
		// dest_id may be a JSON number or string depending on the API; keep raw.
		DestID     json.RawMessage `json:"dest_id"`
		SearchType string          `json:"search_type"`
		Name       string          `json:"name"`
	} `json:"data"`
}

func rawToID(raw json.RawMessage) string {
	return strings.Trim(string(raw), `"`)
}

type bcHotelsResp struct {
	Data struct {
		Hotels []struct {
			Property struct {
				Name           string  `json:"name"`
				ReviewScore    float64 `json:"reviewScore"`
				PriceBreakdown struct {
					GrossPrice struct {
						Value    float64 `json:"value"`
						Currency string  `json:"currency"`
					} `json:"grossPrice"`
				} `json:"priceBreakdown"`
			} `json:"property"`
		} `json:"hotels"`
	} `json:"data"`
}

type hotelResult struct {
	Name     string  `json:"name"`
	Price    float64 `json:"price_per_night"`
	Currency string  `json:"currency"`
	Score    float64 `json:"review_score"`
}

type hotelSearchResult struct {
	Hotels     []hotelResult `json:"hotels"`
	BookingURL string        `json:"booking_url,omitempty"`
	Message    string        `json:"message,omitempty"`
}

func (h *HotelSearch) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p hotelSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Adults <= 0 {
		p.Adults = 2
	}

	// 1. Resolve city -> dest_id.
	destQ := url.Values{"query": {p.Location}}
	var dest bcHotelDestResp
	if err := h.client.getJSON(ctx, "/api/v1/hotels/searchDestination", destQ, &dest); err != nil {
		return nil, fmt.Errorf("resolve location: %w", err)
	}
	destID := ""
	for _, d := range dest.Data {
		if d.SearchType == "city" {
			destID = rawToID(d.DestID)
			break
		}
	}
	if destID == "" && len(dest.Data) > 0 {
		destID = rawToID(dest.Data[0].DestID)
	}
	if destID == "" {
		return json.Marshal(hotelSearchResult{Message: "город не найден: " + p.Location})
	}

	// 2. Search hotels.
	q := url.Values{
		"dest_id":        {destID},
		"search_type":    {"CITY"},
		"arrival_date":   {p.CheckIn},
		"departure_date": {p.CheckOut},
		"adults":         {fmt.Sprintf("%d", p.Adults)},
		"room_qty":       {"1"},
		"page_number":    {"1"},
		"currency_code":  {h.currency},
		"languagecode":   {"en-us"},
	}
	var resp bcHotelsResp
	if err := h.client.getJSON(ctx, "/api/v1/hotels/searchHotels", q, &resp); err != nil {
		return nil, fmt.Errorf("search hotels: %w", err)
	}

	results := make([]hotelResult, 0, len(resp.Data.Hotels))
	for _, ht := range resp.Data.Hotels {
		pr := ht.Property
		price := pr.PriceBreakdown.GrossPrice.Value
		if p.BudgetMax > 0 && price > p.BudgetMax {
			continue
		}
		if p.MinScore > 0 && pr.ReviewScore < p.MinScore {
			continue
		}
		results = append(results, hotelResult{
			Name:     pr.Name,
			Price:    price,
			Currency: pr.PriceBreakdown.GrossPrice.Currency,
			Score:    pr.ReviewScore,
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Price < results[j].Price })
	if len(results) > h.limit {
		results = results[:h.limit]
	}

	out := hotelSearchResult{Hotels: results}
	if len(results) == 0 {
		out.Message = "ничего не найдено по этим параметрам — попробуй другие даты или бюджет"
	} else {
		bq := url.Values{"ss": {p.Location}, "checkin": {p.CheckIn}, "checkout": {p.CheckOut}, "group_adults": {fmt.Sprintf("%d", p.Adults)}}
		out.BookingURL = "https://www.booking.com/searchresults.html?" + bq.Encode()
	}
	return json.Marshal(out)
}
