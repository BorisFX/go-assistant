# Travel search tools (flights + hotels) — design

Date: 2026-06-26
Status: approved, source verified working end-to-end

## Goal

Give both go-assistant bots (@debil4bot / Oleg and @iuriibuildai_bot / Yuri —
shared binary) the ability to search **flights and hotels** with real prices, e.g.
«найди билеты Пномпень–Бангкок на 1 августа и отель там же на 3 ночи».

Two builtin tools: `flight_search` and `hotel_search`. The model calls one or
both depending on the request. Search only — no booking/payments (YAGNI).

## Single source — Booking.com via RapidAPI (VERIFIED)

`booking-com15.p.rapidapi.com` (RapidAPI). Oleg's account is subscribed (free
Basic plan). Auth headers: `x-rapidapi-key`, `x-rapidapi-host`. One key covers
BOTH flights and hotels.

Verified end-to-end 2026-06-26 with the real key — real data, incl. home airport PNH:
- Flights PNH→BKK 2026-08-01 → 15 offers, cheapest Thai AirAsia $95.02 nonstop.
- Hotels Bangkok 2026-08-01..04 → 20 hotels with prices (ARNI Skye $119.83, etc.).

Travelpayouts dropped: its flight cache is sparse (BKK→SIN returned 0; PNH "not
flightable") and its hotel price API is retired. Token kept unused for now.

### flight_search — endpoints
1. `GET /api/v1/flights/searchDestination?query=<city>` → `data[].id`
   (e.g. `PNH.CITY` / `BKK.CITY`; pick `type=="CITY"` or the requested airport).
2. `GET /api/v1/flights/searchFlights?fromId=<id>&toId=<id>&departDate=YYYY-MM-DD`
   `&adults=1&cabinClass=ECONOMY&currency_code=USD&sort=CHEAPEST&pageNo=1`
   (+ optional `returnDate` for round trip) → `data.flightOffers[]`:
   - `priceBreakdown.total.units(+nanos)` + `currencyCode`
   - `segments[].legs[].carriersData[].name`, departure time, stop count.

### hotel_search — endpoints
1. `GET /api/v1/hotels/searchDestination?query=<city>` → `data[].dest_id`
   (pick `search_type=="city"`).
2. `GET /api/v1/hotels/searchHotels?dest_id=<id>&search_type=CITY`
   `&arrival_date=YYYY-MM-DD&departure_date=YYYY-MM-DD&adults=2&room_qty=1`
   `&page_number=1&currency_code=USD&languagecode=en-us` → `data.hotels[]`:
   - `property.name`, `property.priceBreakdown.grossPrice.value`+`.currency`,
     `property.reviewScore`, hotel id (for booking link).

## Target & availability

- New files `internal/tooling/builtin/flight_search.go` and `hotel_search.go`,
  registered in `internal/tooling/registry.go` next to `search_web`.
- Compiled into the shared binary → available to BOTH bots.
- Gated by config: registered only when `travel_search.rapidapi_key` is set
  (obsidian-style gating). Both bots get the key.
- Shared internal helper for the RapidAPI client (headers, GET, JSON decode,
  error handling) reused by both tools.

## Tool interfaces

`flight_search`:
- `origin` (city, required), `destination` (city, required),
  `depart_date` (YYYY-MM-DD, required), `return_date` (optional),
  `adults` (default 1), `max_price` (optional). Sort by price; return top 5–8.

`hotel_search`:
- `location` (city, required), `check_in`, `check_out` (required),
  `adults` (default 2), `budget_max` (optional), `min_score` (optional).
  Sort by price; return top 5–8.

Output → returned to the model, which writes the Telegram reply in Russian.
Empty result → explicit "ничего не найдено" so the bot can ask to relax params.
User-facing strings Russian; code/comments/logs English (project policy).

## Config

```yaml
travel_search:
  rapidapi_key: "***"                 # RapidAPI key (per-server config, NOT committed)
  rapidapi_host: "booking-com15.p.rapidapi.com"
  currency: "USD"
  results_limit: 6
```

Set in both `/opt/assistant/config.yaml` and `/opt/assistant-yuri/config.yaml`.

## Trigger

No hard-coded intercept. Add to BOTH system prompts: «найди билеты …» /
«найди рейс …» → `flight_search`; «найди отель …» / «подбери гостиницу …» →
`hotel_search`; a trip request («слетать в X с … по …») → call both. If
city/dates missing, ask first.

## Testing

`flight_search_test.go` and `hotel_search_test.go` with a mocked HTTP server
(style of `search_web_test.go`): happy path, empty result, price filter. The
shared RapidAPI client gets its own unit test. No live network in tests.

## Deploy

`make deploy` rebuilds the shared binary and restarts BOTH bots (brief). Set the
RapidAPI key in both configs during the same deploy.

## Out of scope

Actual booking, payments, accounts, seat/room selection, multi-currency UX,
NL date parsing inside the tools (model supplies normalized dates).
