# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`tele-gateway` is a Telegram **userbot** (a real user account driven via MTProto, not a Bot API bot) that auto-replies with bids in auction groups when a keyword rule matches, and stops watching a rule when a stop-keyword appears. It also doubles as an **AI gateway** that auto-answers private DMs via Google Gemini. A Gin REST API (consumed by a separate frontend at `D:\Golang\tele-frontend`) manages bid rules, users, groups, and bot lifecycle.

## Commands

```bash
# Run (prompts for Telegram OTP on first login — see OTP note below)
go run cmd/tele-gateway/main.go

# Build
go build -o tele-gateway ./cmd/tele-gateway/main.go

# Tests (CI runs exactly this)
go test -v ./...

# Single package / single test
go test -v ./internal/repository/
go test -v -run TestName ./internal/usecase/
```

There is no linter config committed; use `go vet ./...` and `gofmt`.

## Runtime prerequisites

- **PostgreSQL** — connection built from `DB_*` env vars in [db.go](internal/repository/db.go); schema is created via GORM `AutoMigrate` on startup (no migration files). TimeZone is hardcoded to `Asia/Jakarta`, `sslmode=disable`.
- **`.env`** — copy `.env.example`. Requires Telegram `TELEGRAM_APP_ID`/`TELEGRAM_APP_HASH` (from my.telegram.org), `PHONE_NUMBER`, optional 2FA `PASSWORD`, `JWT_SECRET`/`JWT_REFRESH_SECRET`, `GEMINI_API_KEY`, `ADMIN_EMAIL`/`ADMIN_PASSWORD`/`ADMIN_NAME` (seeds the first admin user), and `ALLOWED_ORIGINS` (comma-separated CORS list).
- **OTP on first login**: the userbot needs a Telegram login code. `main.go` wires an `otpProvider` that blocks on `WaitOTP` (5-min timeout) — submit the code via `POST /api/bot/otp`, or fall back to console stdin (`docker attach` in production). Session is persisted to `TELEGRAM_SESSION_FILE` so this only happens once.

## Architecture

Clean Architecture with strict inward dependencies. Interfaces are declared in `internal/domain`; `usecase` depends only on those interfaces; `delivery` and `repository` are the outer adapters. Wiring happens manually in [main.go](cmd/tele-gateway/main.go) (no DI framework).

```
domain/      Entities + interface contracts (BidRule, User, TelegramGroup, AIContext + their Repo/UseCase interfaces)
usecase/     Business logic — depends on domain interfaces only
repository/  GORM/Postgres implementations of domain repositories
delivery/
  http/      Gin REST API (handlers, router, JWT + role middleware)
  telegram/  gotd MTProto client + update handler (the userbot itself)
seeder/      Creates the default admin from ADMIN_* env vars on boot
config/      Env loading (godotenv) + zap logger
```

### Two concurrent entry surfaces

`main.go` runs two things in one process:
1. **HTTP server** on `:8080` (background goroutine) — the management API.
2. **Telegram client** (`tgClient.Start`, blocking on the main goroutine) — the userbot event loop, driven by `context` from `signal.NotifyContext` for graceful shutdown.

The `auctionUseCase` is shared between both and holds bot **status** (`IDLE` → `WAITING_OTP` → `RUNNING`) plus the OTP channel bridging the HTTP `SubmitOTP` call to the Telegram auth flow's `WaitOTP`.

### Telegram update handling ([message_handler.go](internal/delivery/telegram/message_handler.go))

`AuctionHandler.Handle` is the gotd `UpdateHandler`. gotd delivers updates in **several shapes** that must each be unpacked separately — `*tg.Updates`, `*tg.UpdatesCombined`, `*tg.UpdateShort`, and `*tg.UpdateShortMessage`. Only the first two carry `entities` (users/channels with `AccessHash`); `UpdateShort*` variants arrive without them, so peer resolution falls back to `AccessHash: 0` and hopes the sender library resolves it. When adding update handling, remember to cover all these cases.

`OnNewMessage` routes by peer type:
- **Private message (`PeerUser`)** → AI gateway (`AIUseCase.HandlePrivateMessage`), run in a background goroutine so the update loop never blocks.
- **Group/channel (`PeerChat`/`PeerChannel`)** → bidding logic: first `CheckAndStopByText` (deactivate rules whose stop-keyword appears), then `CheckKeyword`. On a match, the bid is sent from a goroutine after a **random 2–5s delay** (anti-ban human simulation).

### Keyword matching ([bid_repository.go](internal/repository/bid_repository.go) `GetActiveRuleByKeyword`)

This is the core auction logic and the most subtle part of the codebase:
- A rule's `Keyword` is a **comma-separated list of patterns, ALL of which must match** (logical AND) for the rule to fire.
- Each pattern is compiled as a **case-insensitive Go regex** (`(?i)` prefix); if compilation fails it falls back to case-insensitive substring `Contains`.
- **Topic scoping**: `topic_id = 0` means the rule is global for the group; a message in topic N matches rules with `topic_id = N OR topic_id = 0`. Topic-specific rules are ordered first (`topic_id desc`).
- `HasBidded` guards against double-bidding; `ExecuteBid` **re-fetches the rule** right before sending to avoid racing the delay window against a concurrent deactivation.
- Go's `regexp` is RE2 — **no backreferences or lookaround**. The example schedule regex `^(Jadwal\s*:\s*)?(Senin|Monday|Mon)\s+(pukul\s+)?19([.:]00)?\s*(WIB)?$` works because it's pure RE2. Note matching is against the **whole message text** (`re.MatchString`), so an anchored `^...$` pattern only fires on single-line messages — for multi-line auction posts, avoid `$` anchoring or the pattern won't match.
- `Create` recovers soft-deleted rows: because `Keyword` has a unique index, re-creating a previously deleted keyword un-deletes and overwrites the old row rather than erroring.

### AI gateway ([ai_gateway_usecase.go](internal/usecase/ai_gateway_usecase.go))

Per-sender **debounced batching**: incoming DMs are queued per `senderID` in a `sync.Map` of `userQueue`. A 60-second debounce timer resets on each new message; when it fires, up to **10 messages** are joined and sent to Gemini in one call, with a follow-up 10s timer draining any overflow. This coalesces rapid-fire user messages into a single AI turn.

Gemini specifics: uses `google.golang.org/genai` with `BackendGeminiAPI`, model `gemini-3.1-flash-lite`. The system prompt is loaded from the `AIContext` DB row keyed `system_prompt` and passed via **`SystemInstruction`** (deliberately NOT as user content) to prevent prompt leakage. On any API error or empty response the bot **stays silent** rather than replying — do not add error/fallback replies to the user. Responses are suffixed with `[Dibalas oleh Azhar's AI Assistant]`.

### HTTP API ([router.go](internal/delivery/http/router.go))

All routes under `/api`. Public: `POST /login`, `POST /refresh`. Everything else is behind `AuthMiddleware` (JWT Bearer, HS256, `JWT_SECRET`). Access tokens live 15 min, refresh tokens 7 days (`JWT_REFRESH_SECRET`). Mutating rule/user/bot/AI-context routes additionally require `RoleMiddleware(RoleAdmin)`.

Roles are string constants: `RoleAdmin = "Admin"`, `RoleUser = "Siswa"` (the app's domain is a tutoring/course context). New users default to `Siswa`.

## Conventions

- Code comments and log messages are a mix of **English and Indonesian** — match the surrounding file.
- Every file starts with a `// path/to/file.go` header comment.
- Logging is structured **zap** throughout (`zap.String`, `zap.Error`, …); loggers are injected, not global.
- Errors bubble up wrapped with `fmt.Errorf("...: %w", err)`; handlers translate to JSON `gin.H{"error": ...}`.

## Deployment

Multi-stage [Dockerfile](Dockerfile) → static binary on Alpine, session persisted to the `/app/session` volume. [docker-compose.yml](docker-compose.yml) expects an **external** Docker network `shared-network` and shares a Postgres from another stack — `stdin_open`/`tty` are enabled so you can `docker attach tele-gateway` to enter the first-login OTP. CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs `go test ./...`, then on `main` builds/pushes the image to Docker Hub and SSH-deploys to a VPS.
