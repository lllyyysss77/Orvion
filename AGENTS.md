# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project

Orvion — a multi-provider LLM gateway (Go 1.25 + Gin + GORM) that exposes OpenAI/Anthropic-compatible endpoints, with an embedded React admin WebUI. Traffic from `/v1/*` is load-balanced across one or more upstream providers bound to each logical model, with retry, breaker, and per-AuthKey RPM limiting.

## Common commands

```bash
make tidy      # go mod tidy
make fmt       # go fmt ./...
make webui     # cd webui && npm install && npm run build  (required before `go build`/`go run`)
make run       # go run .
make all       # make webui && make run
```

- The Go binary will **not compile without `webui/dist`** — `router.go` uses `//go:embed webui/dist` + `//go:embed webui/dist/index.html`. Run `make webui` first after a fresh clone or after deleting `webui/dist`.
- WebUI dev server (hot-reload, proxies to backend): `cd webui && npm run dev`.
- WebUI lint: `cd webui && npm run lint`.
- Go tests: `go test ./...`. There is no `make test` target.
- Version injection: `go build -ldflags "-X github.com/racio/orvion/consts.Version=vX.Y.Z" -o orvion .`
- Default local URL: `http://127.0.0.1:7070/` (WebUI, health, `/v1/*`, `/api/*`).

## Architecture

### Request flow (`/v1/*`)

1. `router.go` mounts OpenAI-style handlers (`/v1/chat/completions`, `/responses`, `/embeddings`, `/rerank`, `/images/*`) behind `middleware.AuthOpenAI`, and Anthropic-style handlers (`/v1/messages`, `/v1/messages/count_tokens`) behind `middleware.AuthAnthropic`. Auth accepts either the admin `TOKEN` or an `AuthKey` row.
2. `handler/chat.go::chatHandler` is the shared entry point. Each protocol passes its own `service.Beforer` (parses request, flags stream/tool/image/structured) and `service.Processer` (parses upstream response, extracts usage).
3. `service.ProvidersWithMetaBymodelsName` resolves the logical `Model` → enabled `ModelWithProvider` rows → `Provider` rows, and returns `WeightItems`, `MaxRetry`, `TimeOut`, `Strategy`, `Breaker`, `IOLog`.
4. `service.BalanceChatWithLimiter` (`service/chat.go`) drives the retry loop:
   - Picks an upstream via `service/runtime.NewBalancer` (strategy from `consts.BalancerLottery` / `BalancerRotor`, optionally wrapped by `balancers.breaker`).
   - Builds headers via `runtimesvc.BuildHeaders` (passthrough + custom headers + stream hint).
   - Instantiates a per-style upstream client with `providers.NewForStyle(style, provider.Config)` and `BuildReq`.
   - Per-provider budget: `perProviderMaxAttempts = 2` — retries same provider for network/5xx/429/408, then `balancer.Reduce` on 429 or `balancer.Delete` on other failures before switching.
   - Non-retryable 4xx (see `runtimesvc.IsRetryableStatus`) skips to the next provider immediately.
   - RPM is recorded **before** the upstream call so failed requests still count (prevents limit-bypass via forced errors).
5. On success, the response body streams to the client and `service.RecordLog` reads it concurrently to fill `ChatLog` + optional `ChatIO`, compute usage, apply `estimateUsageFromIO` fallback only when usage is missing, and call `runtimesvc.CalculateTotalCost` against `ModelPrice`.

### Provider abstraction (`providers/`)

`providers.Provider` is a two-method interface: `BuildReq(ctx, header, model, rawBody) (*http.Request, error)` and `Models(ctx) ([]Model, error)`. Implementations:
- `OpenAI` (`openai.go`) — default; styles `openai` / `openai-embeddings`.
- `OpenAIRes` (`openai_res.go`) — OpenAI `/responses` style (`codex`).
- `Anthropic` (`anthropic.go`) — `anthropic` style, `x-api-key`.
- `Gemini` (`gemini.go`) — `gemini` / `gemini-embeddings` styles.

Style selection: `ResolveStyle(preferredStyle, providerConfig)` — if style is empty, infer from `base_url` (anthropic / googleapis → gemini / else openai). When editing provider code, add the style constant to `consts/consts.go` and register it in the `switch` inside `providers.NewForStyle`.

### Gemini compat layer

`service/before.go::BeforerOpenAI` will, when `model` starts with `gemini` and `GEMINI_COMPAT_ENABLED` is on (default true), strip `patternProperties` from tool schemas and normalize tool-call history to work around upstream gateways that reject empty `function_response.name`. Keep this change minimal — prior commits (`4e4ebfc`, `a0ac226`) explicitly rolled back broader rewrites.

### Load balancing & breaker (`balancers/`)

- `Lottery` — weighted random; `Reduce` lowers its weight and defers it while unreduced providers remain; `Delete` removes it.
- `Rotor` — round-robin via `container/list`, `Reduce` moves to back, `Delete` removes.
- `balancers/breaker.go` wraps either strategy when `Model.Breaker == 1`. Consecutive errors on a `ModelWithProvider` trigger auto-disable (`AutoDisabledUntil`); `service.StartModelProviderAutoRecovery` + `RestoreExpiredAutoDisabledModelProviders` re-enable them on cooldown expiry. Errors recorded via `SaveChatLog` also trigger `TriggerModelProviderAutoDisableIfNeeded` asynchronously.

### Database (`models/`)

- Default SQLite at `./data/llmio.db`, overridable via `DATABASE_DSN` (supports `sqlite://`, raw path, `file:`, and Postgres URL / key=value DSN — `models.isPostgresDSN` detects).
- `models.Init` and monthly chat-log provisioning call GORM `AutoMigrate`. Treat schema changes as production migrations: verify generated changes against every supported database and use explicit `Migrator()` operations when a controlled rollout is required.
- Boolean-like fields are stored as `int` (0/1): `Model.Status`, `Model.IOLog`, `Model.Breaker`, `ModelWithProvider.Status`, `ModelWithProvider.WithHeader`, `AuthKey.Status`, `AuthKey.AllowAll`, `ChatLog.ChatIO`. Query/compare against `1`/`0`, not bool.
- `ChatLog.UUID` is `NOT NULL UNIQUE`; `SaveChatLog` retries on SQLSTATE 23505.

### WebUI (`webui/`)

React 19 + Vite + TypeScript + TailwindCSS v4 + Radix UI + react-router v7. Pages live under `src/routes/`, API client in `src/lib/api.ts`, auth helpers in `src/lib/auth.ts`. Build output (`webui/dist/`) is consumed via `go:embed`; the SPA fallback in `router.go::setWebUIRoutes` serves `index.html` for any non-`/api`, non-`/v1`, non-`/auth-key`, non-`/health` GET.

### Background workers

`main.go::startBackgroundWorkers` launches four goroutines (each owns its own ticker, listens on the root context):
- `StartPriceSync` — syncs model prices (table `model_prices`).
- `StartSystemLogCleanup` — retention cleanup for system logs.
- `StartModelProviderAutoRecovery` — flips `AutoDisabledUntil` rows back on.
- `StartTelegramCommandBot` — long-poll TG bot (`/status`, `/model`, `/help`); config from env or `configs.breaker_alert_tg`.

## Conventions

- Module path is `github.com/racio/orvion` (note: `orvion`, not `llmio`, despite the repo dir name).
- Commit messages are Conventional-Commits-style with Chinese descriptions (e.g. `feat(gateway): ...`, `fix(gemini-compat): ...`). Scope matches the top-level package / feature area.
- Log via `log/slog` (key-value pairs). `pkg/logutil.NewSystemLogWriter` tees into the system-log store so entries show up in the WebUI `system-logs` page; don't bypass it with raw `os.Stdout` writes.
- Trust-proxy is **off by default** — set `TRUSTED_PROXIES` when running behind Nginx/CF, otherwise `c.ClientIP()` won't honor `X-Forwarded-For`.
- When `TOKEN` is empty, `/api/*` skips admin auth entirely — useful for local dev, but never ship a deployment that way.
