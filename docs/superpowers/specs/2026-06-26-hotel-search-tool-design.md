# Hotel search tool — design

Date: 2026-06-26
Status: approved (pending spec review)

## Goal

Add a `hotel_search` builtin tool to go-assistant so both bot instances
(@debil4bot / Oleg and @iuriibuildai_bot / Yuri — shared binary) can answer
free-text requests like «найди отель в Бангкоке с 1 по 5 июля на двоих до $60»
with a ranked list of hotels (name, stars, price/night, booking link).

Search only — no booking, accounts, or payments (YAGNI).

## Target & availability

- New file `internal/tooling/builtin/hotel_search.go`, registered in
  `internal/tooling/registry.go` next to `search_web`.
- Compiled into the single shared binary → automatically available to BOTH bots.
- Gated by config: registered only when `hotel_search.token` is set
  (same pattern as the obsidian tool gating). Both bots get the token →
  both expose the tool.

## Data source — Travelpayouts (Hotellook)

- Provider: Travelpayouts affiliate network (Aviasales / Hotellook).
- Token: already owned (obtained for flights). Stored in config, NOT in repo.
- Affiliate `marker`: optional. Without it search still works; with it the
  booking links carry attribution and earn commission. Add later.

### KNOWN RISK — verify at implementation time
- The legacy documented endpoint `engine.hotellook.com/api/v2/cache.json`
  and `/lookup.json` return **404** (tested 2026-06-26, http→https, with token).
  The hotels data API has moved. Implementation must target the **current
  Travelpayouts hotels API** (likely `https://api.travelpayouts.com/...`) per
  live docs at travelpayouts.github.io/slate and the help center.
- The token was issued for the flights program. Confirm it has **Hotels data
  API access**; if not, enable the Hotels/Hotellook program in the Travelpayouts
  cabinet. Validate with one real call before wiring the tool.

## Tool interface

Input parameters (LLM-supplied):
- `location` (string, required) — city or area name.
- `check_in` (string, required) — YYYY-MM-DD.
- `check_out` (string, required) — YYYY-MM-DD.
- `adults` (int, default 2).
- `budget_max` (number, optional) — max price/night, currency USD.
- `min_stars` (int, optional).

Behavior:
1. Resolve `location` → location id (lookup call).
2. Query hotel prices for the dates.
3. Filter by `budget_max` / `min_stars` if provided; sort by price ascending.
4. Return top 5–8.

Output (returned to the model, which formats the Telegram reply in Russian):
- For each hotel: name, stars, price/night (USD), booking URL (with `marker`
  when configured).
- Empty result → clear "ничего не найдено по этим параметрам" signal so the
  bot can ask the user to relax dates/budget.

User-facing strings: Russian. Code, comments, logs: English (project policy).

## Config

```yaml
hotel_search:
  token: "***"        # Travelpayouts API token (set per server config, not committed)
  marker: ""          # optional affiliate id
  currency: "usd"     # default
  results_limit: 6
```

Set in both `/opt/assistant/config.yaml` and `/opt/assistant-yuri/config.yaml`.

## Trigger

No hard-coded intercept (unlike legal-review «разбери папку»). Instead, add a
short instruction to BOTH system prompts: phrases like «найди отель …»,
«подбери гостиницу …» → call `hotel_search`; if city or dates are missing, ask
the user before calling.

## Testing

- `internal/tooling/builtin/hotel_search_test.go` with a mocked HTTP server
  (same style as `search_web_test.go`): one happy-path, one empty-result,
  one filter (budget/stars) case. No live network in tests.

## Deploy

- Adding Go code requires `make deploy`, which rebuilds the shared binary and
  **restarts both bots** (brief). Acceptable here — the feature is wanted for
  both. Set the config token on both instances during the same deploy.

## Out of scope

- Actual booking, payment, user accounts, multi-currency UX, date parsing from
  natural language inside the tool (the model supplies normalized dates).
