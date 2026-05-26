# Go Assistant

Personal AI assistant built in Go with clean architecture (Hexagonal / Ports & Adapters). Single binary, multi-instance — run multiple independent bots from one codebase with different configs.

## Features

- **Telegram bot** — draft streaming, polling watchdog, per-chat sequencing, debouncer
- **Lazy tool loading** — LLM receives only tool names (~300 tokens), full schemas loaded on demand. 90% token savings vs traditional approach
- **Pluggable tools** — search_web, bash, cloud_files (WebDAV), trading monitor, Claude Code CLI
- **Multimodal** — vision (GPT-4o-mini), voice transcription (Whisper via OpenRouter), document analysis (PDF/DOC/XLS)
- **Memory** — three-tier: short-term (RAM), working (pgvector), long-term (daily summaries + fact extraction)
- **Cron scheduler** — periodic tasks stored in PostgreSQL, managed via Telegram commands
- **Web dashboard** — React SPA embedded in Go binary (chat, activity, memory, settings)
- **Multi-instance** — same binary, different configs. Per-instance: database, system prompt, tools, Telegram bot
- **Configurable LLM** — any OpenRouter model (DeepSeek, Claude, Gemini, GPT-4o, etc.)

## Architecture

Hexagonal (Ports & Adapters) modular monolith.

```
cmd/assistant/        — entry point, DI wiring
internal/domain/      — entities, value objects, events (zero dependencies)
internal/port/        — interfaces (input + output ports)
internal/app/         — use cases (chat pipeline, classifier, memory, cron)
internal/adapter/
  driven/             — outbound (OpenRouter, SearXNG, PostgreSQL, WebDAV, Claude Code)
  driving/            — inbound (Telegram bot, HTTP API)
internal/tooling/     — tool registry with lazy schema loading
  builtin/            — built-in tools (search, bash, cloud files, trading)
pkg/config/           — YAML config with env var expansion
```

## Quick Start

```bash
cp configs/config.example.yaml configs/config.yaml
# Edit config.yaml with your tokens

make build
./bin/assistant --config=configs/config.yaml
```

### Prerequisites

- Go 1.22+
- PostgreSQL 16 with pgvector extension
- Telegram bot token (from @BotFather)
- OpenRouter API key

### Optional

- SearXNG (Docker) for self-hosted search
- Node.js 20+ for dashboard development

## Tools

| Tool | Description |
|------|-------------|
| `search_web` | Internet search via SearXNG with DuckDuckGo fallback |
| `bash` | Execute shell commands on the server |
| `cloud_files` | Mail.ru Cloud WebDAV — list, search, read, download, upload |
| `trading_status` | Monitor CryptoAI trading bot |
| `run_code` | Execute Claude Code CLI for coding tasks |

## Telegram Commands

```
/help          — list commands
/status        — trading bot status
/code <prompt> — run Claude Code
/memory        — show stored memories
/cron list     — show scheduled tasks
/cron add <schedule> | <prompt> — add periodic task
/cron del <N>  — delete task
```

## Multi-Instance

Run multiple independent bots from the same binary:

```bash
# Instance 1
./assistant --config=/opt/bot1/config.yaml

# Instance 2
./assistant --config=/opt/bot2/config.yaml
```

Each instance has its own database, Telegram bot, system prompt, and tool configuration. Instances share nothing.

## Build & Deploy

```
make build      — build binary (with embedded dashboard)
make build-go   — build Go only (skip dashboard)
make test       — run tests with race detection
make deploy     — cross-compile for Linux, upload, restart service
make lint       — run golangci-lint
```

## Tech Stack

- **Go 1.22+** — Hexagonal Architecture, DDD
- **PostgreSQL 16 + pgvector** — persistence, vector similarity search
- **OpenRouter** — LLM provider (any model), embeddings, speech-to-text
- **SearXNG** — self-hosted metasearch engine
- **React 18 + TypeScript + Tailwind** — embedded dashboard
- **go-telegram-bot-api v5** — Telegram integration
- **go-chi/chi v5** — HTTP router

## License

MIT
