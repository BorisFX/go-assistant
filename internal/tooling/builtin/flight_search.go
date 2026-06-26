package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

// FlightSearch finds flights with prices via booking-com15 on RapidAPI.
type FlightSearch struct {
	client   *RapidAPIClient
	currency string
	limit    int
}

func NewFlightSearch(client *RapidAPIClient, currency string, limit int) *FlightSearch {
	if currency == "" {
		currency = "USD"
	}
	if limit <= 0 {
		limit = 6
	}
	return &FlightSearch{client: client, currency: currency, limit: limit}
}

func (f *FlightSearch) Name() string { return "flight_search" }
func (f *FlightSearch) Description() string {
	return "Search flights with live prices between two cities for given dates"
}
func (f *FlightSearch) Category() string { return "travel" }

func (f *FlightSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"origin": {"type": "string", "description": "Origin city, e.g. Phnom Penh"},
			"destination": {"type": "string", "description": "Destination city, e.g. Bangkok"},
			"depart_date": {"type": "string", "description": "Departure date, format YYYY-MM-DD"},
			"return_date": {"type": "string", "description": "Optional return date for round trip, YYYY-MM-DD"},
			"adults": {"type": "integer", "description": "Number of adults", "default": 1},
			"max_price": {"type": "number", "description": "Optional max total price"}
		},
		"required": ["origin", "destination", "depart_date"]
	}`)
}

type flightSearchParams struct {
	Origin      string  `json:"origin"`
	Destination string  `json:"destination"`
	DepartDate  string  `json:"depart_date"`
	ReturnDate  string  `json:"return_date"`
	Adults      int     `json:"adults"`
	MaxPrice    float64 `json:"max_price"`
}

type bcFlightDestResp struct {
	Data []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"data"`
}

type bcFlightsResp struct {
	Data struct {
		FlightOffers []struct {
			PriceBreakdown struct {
				Total struct {
					Units        int64  `json:"units"`
					Nanos        int64  `json:"nanos"`
					CurrencyCode string `json:"currencyCode"`
				} `json:"total"`
			} `json:"priceBreakdown"`
			Segments []struct {
				DepartureTime string `json:"departureTime"`
				Legs          []struct {
					CarriersData []struct {
						Name string `json:"name"`
					} `json:"carriersData"`
				} `json:"legs"`
			} `json:"segments"`
		} `json:"flightOffers"`
	} `json:"data"`
}

type flightResult struct {
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Airline  string  `json:"airline"`
	DepartAt string  `json:"depart_at"`
	Stops    int     `json:"stops"`
}

type flightSearchResult struct {
	Flights []flightResult `json:"flights"`
	Message string         `json:"message,omitempty"`
}

func (f *FlightSearch) resolveID(ctx context.Context, query string) (string, error) {
	var dest bcFlightDestResp
	if err := f.client.getJSON(ctx, "/api/v1/flights/searchDestination", url.Values{"query": {query}}, &dest); err != nil {
		return "", err
	}
	for _, d := range dest.Data {
		if d.Type == "CITY" {
			return d.ID, nil
		}
	}
	if len(dest.Data) > 0 {
		return dest.Data[0].ID, nil
	}
	return "", nil
}

func (f *FlightSearch) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p flightSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Adults <= 0 {
		p.Adults = 1
	}

	fromID, err := f.resolveID(ctx, p.Origin)
	if err != nil {
		return nil, fmt.Errorf("resolve origin: %w", err)
	}
	toID, err := f.resolveID(ctx, p.Destination)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}
	if fromID == "" || toID == "" {
		return json.Marshal(flightSearchResult{Message: "не удалось определить аэропорт отправления или назначения"})
	}

	q := url.Values{
		"fromId":        {fromID},
		"toId":          {toID},
		"departDate":    {p.DepartDate},
		"adults":        {fmt.Sprintf("%d", p.Adults)},
		"cabinClass":    {"ECONOMY"},
		"currency_code": {f.currency},
		"sort":          {"CHEAPEST"},
		"pageNo":        {"1"},
	}
	if p.ReturnDate != "" {
		q.Set("returnDate", p.ReturnDate)
	}

	var resp bcFlightsResp
	if err := f.client.getJSON(ctx, "/api/v1/flights/searchFlights", q, &resp); err != nil {
		return nil, fmt.Errorf("search flights: %w", err)
	}

	results := make([]flightResult, 0, len(resp.Data.FlightOffers))
	for _, o := range resp.Data.FlightOffers {
		price := float64(o.PriceBreakdown.Total.Units) + float64(o.PriceBreakdown.Total.Nanos)/1e9
		if p.MaxPrice > 0 && price > p.MaxPrice {
			continue
		}
		airline, departAt, stops := "", "", 0
		if len(o.Segments) > 0 {
			seg := o.Segments[0]
			departAt = seg.DepartureTime
			if len(seg.Legs) > 0 {
				stops = len(seg.Legs) - 1
				if len(seg.Legs[0].CarriersData) > 0 {
					airline = seg.Legs[0].CarriersData[0].Name
				}
			}
		}
		results = append(results, flightResult{
			Price:    price,
			Currency: o.PriceBreakdown.Total.CurrencyCode,
			Airline:  airline,
			DepartAt: departAt,
			Stops:    stops,
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Price < results[j].Price })
	if len(results) > f.limit {
		results = results[:f.limit]
	}

	out := flightSearchResult{Flights: results}
	if len(results) == 0 {
		out.Message = "рейсы не найдены — попробуй другую дату или соседний город"
	}
	return json.Marshal(out)
}
