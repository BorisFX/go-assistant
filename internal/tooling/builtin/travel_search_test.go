package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(srv *httptest.Server) *RapidAPIClient {
	c := NewRapidAPIClient("test-key", "booking-com15.p.rapidapi.com")
	c.baseURL = srv.URL
	return c
}

func TestHotelSearch_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hotels/searchDestination":
			if got := r.Header.Get("x-rapidapi-key"); got != "test-key" {
				t.Errorf("missing key header, got %q", got)
			}
			w.Write([]byte(`{"data":[{"dest_id":-3414440,"search_type":"city","name":"Bangkok"}]}`))
		case "/api/v1/hotels/searchHotels":
			if r.URL.Query().Get("dest_id") != "-3414440" {
				t.Errorf("wrong dest_id: %s", r.URL.Query().Get("dest_id"))
			}
			w.Write([]byte(`{"data":{"hotels":[
				{"property":{"name":"Pricey","reviewScore":9.3,"priceBreakdown":{"grossPrice":{"value":400.42,"currency":"USD"}}}},
				{"property":{"name":"Cheap","reviewScore":8.5,"priceBreakdown":{"grossPrice":{"value":119.83,"currency":"USD"}}}}
			]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tool := NewHotelSearch(newTestClient(srv), "USD", 6)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"location":"Bangkok","check_in":"2026-08-01","check_out":"2026-08-04","adults":2}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res hotelSearchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Hotels) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(res.Hotels))
	}
	if res.Hotels[0].Name != "Cheap" {
		t.Errorf("expected cheapest first, got %s", res.Hotels[0].Name)
	}
	if res.BookingURL == "" {
		t.Errorf("expected a booking url")
	}
}

func TestHotelSearch_BudgetFilterEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hotels/searchDestination":
			w.Write([]byte(`{"data":[{"dest_id":"123","search_type":"city","name":"Bangkok"}]}`))
		case "/api/v1/hotels/searchHotels":
			w.Write([]byte(`{"data":{"hotels":[{"property":{"name":"Pricey","reviewScore":9,"priceBreakdown":{"grossPrice":{"value":400,"currency":"USD"}}}}]}}`))
		}
	}))
	defer srv.Close()

	tool := NewHotelSearch(newTestClient(srv), "USD", 6)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"location":"Bangkok","check_in":"2026-08-01","check_out":"2026-08-04","budget_max":100}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res hotelSearchResult
	json.Unmarshal(raw, &res)
	if len(res.Hotels) != 0 {
		t.Fatalf("expected 0 hotels under budget, got %d", len(res.Hotels))
	}
	if res.Message == "" {
		t.Errorf("expected an empty-result message")
	}
}

func TestFlightSearch_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/flights/searchDestination":
			q := r.URL.Query().Get("query")
			if q == "Phnom Penh" {
				w.Write([]byte(`{"data":[{"id":"PNH.CITY","type":"CITY","code":"PNH","name":"Phnom Penh"}]}`))
			} else {
				w.Write([]byte(`{"data":[{"id":"BKK.CITY","type":"CITY","code":"BKK","name":"Bangkok"}]}`))
			}
		case "/api/v1/flights/searchFlights":
			if r.URL.Query().Get("fromId") != "PNH.CITY" || r.URL.Query().Get("toId") != "BKK.CITY" {
				t.Errorf("wrong ids: %s -> %s", r.URL.Query().Get("fromId"), r.URL.Query().Get("toId"))
			}
			w.Write([]byte(`{"data":{"flightOffers":[
				{"priceBreakdown":{"total":{"units":99,"nanos":710000000,"currencyCode":"USD"}},"segments":[{"departureTime":"2026-08-01T10:55:00","legs":[{"carriersData":[{"name":"Air Cambodia"}]}]}]},
				{"priceBreakdown":{"total":{"units":95,"nanos":20000000,"currencyCode":"USD"}},"segments":[{"departureTime":"2026-08-01T18:35:00","legs":[{"carriersData":[{"name":"Thai AirAsia"}]}]}]}
			]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tool := NewFlightSearch(newTestClient(srv), "USD", 6)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"origin":"Phnom Penh","destination":"Bangkok","depart_date":"2026-08-01"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var res flightSearchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Flights) != 2 {
		t.Fatalf("expected 2 flights, got %d", len(res.Flights))
	}
	if res.Flights[0].Airline != "Thai AirAsia" {
		t.Errorf("expected cheapest (Thai AirAsia) first, got %s @ %.2f", res.Flights[0].Airline, res.Flights[0].Price)
	}
	if res.Flights[0].Stops != 0 {
		t.Errorf("expected nonstop, got %d stops", res.Flights[0].Stops)
	}
}

func TestTravelTools_Metadata(t *testing.T) {
	c := NewRapidAPIClient("k", "h")
	if NewFlightSearch(c, "", 0).Name() != "flight_search" {
		t.Error("flight tool name")
	}
	if NewHotelSearch(c, "", 0).Name() != "hotel_search" {
		t.Error("hotel tool name")
	}
	for _, s := range []json.RawMessage{NewFlightSearch(c, "", 0).Schema(), NewHotelSearch(c, "", 0).Schema()} {
		var m map[string]any
		if err := json.Unmarshal(s, &m); err != nil {
			t.Errorf("invalid schema: %v", err)
		}
	}
}
