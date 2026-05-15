# Memory

## Current Task

Step 1 bootstrap: project structure and backend foundation (config, store, auth, rpc tiers, notifications, Jito skeleton, WS priority queue skeleton).

## Progress Summary

- Initialized monorepo structure for backend, frontend, tests, docs, and deployment assets.
- Added root bootstrapping files (`go.mod`, `.env.example`, `Memory.md`).
- Started implementation of backend foundational modules and app entrypoint.

## Key Decisions

- Go + Fiber + zerolog for low-latency service foundations.
- SQLite as the default durable persistence layer for easy local setup and restart safety.
- Explicit tiered RPC manager abstraction to enforce listener-aware routing (Tier A vs Tier B).
- Dry-run defaults ON for sniping risk mitigation.

## Open Questions

- Which exact free-tier RPC/WS endpoints should be bundled as defaults for resilience?
- Which Jito block-engine region should be preferred for deployment geography?
- Should Telegram formatting differ by user risk profile level?

## Next Steps

- Finalize and compile backend foundational modules.
- Add initial SQL migration files for listeners/events/snipe history.

## Checkpoint 2026-05-14 Implementation Continuation

### Current Task

Continue implementation after dependency security gate: normalize module/imports, finish missing frontend scaffold, and validate build/test health.

### Progress Summary

- Updated Go dependency pins to safer versions and ran `go mod tidy`.
- Aligned backend and test import paths to module `github.karma-234/sol-whisperer-v1`.
- Created missing frontend files (`web/package.json`, Vite/TS config, app shell and CSS).
- Installed npm dependencies and confirmed `npm audit` reports 0 vulnerabilities.
- Fixed Fiber shutdown compatibility issue and validated with `go test ./...`.
- Confirmed frontend compiles successfully with `npm run build`.

### Key Decisions

- Upgraded flagged dependencies: Fiber to `v2.52.13`, JWT to `v5.3.1`, and Vite to `v8.0.13`.
- Kept dry-run-first safety posture and prominent risk warning in dashboard shell.

### Open Questions

- Decide whether to lock npm versions tighter (exact pins) versus caret ranges for patch/minor drift.
- Select final free-tier RPC endpoint list for Tier A and Tier B defaults.

### Next Steps

- Implement PumpDev stream ingestion and persistence-backed volume snapshots.
- Add listener orchestration that routes watched tokens to Tier A automatically.
- Expose first websocket endpoint wiring Hub + PriorityQueue to real client sessions.

## Checkpoint 2026-05-14 Post-Update Advisory Verification

### Current Task

Re-check upgraded dependency versions against OSV before proceeding further.

### Progress Summary

- Queried OSV `querybatch` for all upgraded Go and npm pins.
- All queried upgraded versions returned `0` advisories.

### Key Decisions

- Security gate is satisfied for current dependency baseline; implementation can continue.

### Open Questions

- None for dependency security at this checkpoint.

### Next Steps

- Proceed with step-2 implementation: PumpDev websocket ingestion, rolling volume persistence, and spike event emission.
- Add unit tests for WS priority queue and volume tracker baseline structures.
- Scaffold frontend Vite + React baseline with themed CSS shell.

## Checkpoint 2026-05-14 Dependency Security Review

### Current Task

Validate pinned dependency versions against current vulnerability/advisory feeds before installing anything.

### Progress Summary

- Queried OSV batch API for planned Go and npm dependencies.
- Found advisories affecting pinned versions of Fiber (`v2.52.5`), JWT (`v5.2.1`), and Vite (`v6.0.1`).
- Verified latest upstream versions for all planned dependencies.

### Key Decisions

- Do not install dependencies until version pins are updated to safer versions.
- Prefer patched/latest stable versions where advisories have known fixed ranges.

### Open Questions

- For Vite, should we jump to latest major (`8.x`) immediately or pin to a narrower vetted range based on frontend compatibility constraints?

### Next Steps

- Update Go and web dependency pins to patched/latest versions.
- Re-run advisory checks after pin updates.
- Continue implementation only after the dependency baseline is security-reviewed.

## Checkpoint 2026-05-14 Step-2 Volume Ingestion

### Current Task

Implement PumpDev ingestion pipeline with rolling volume persistence and spike emission hooks.

### Progress Summary

- Added persistent store support for `spike_events` plus write/read methods for snapshots and spikes.
- Implemented `internal/volume/processor.go` to consume PumpDev events, evaluate spikes, persist snapshots, persist spikes, and invoke callbacks.
- Extended PumpDev client with deterministic `MockStream` to keep development pipeline active before full external WS integration.
- Wired processor startup into API bootstrap (`cmd/api/main.go`) with:
  - background processing goroutine,
  - websocket broadcast payload on detected spikes (P3),
  - Telegram notification hook,
  - `/api/v1/spikes/recent` endpoint backed by SQLite.
- Added unit test coverage for processor behavior in `tests/unit/volume_processor_test.go`.
- Ran `gofmt` and verified with `go test ./...` (all passing).

### Key Decisions

- Keep real PumpDev connect path in place while enabling mock event stream in non-production mode for fast integration testing.
- Persist every processed 5-minute snapshot to favor recoverability and analytics over minimal write volume at this stage.
- Apply per-mint spike emission throttle (`MinSpikeEmitInterval`) to reduce noisy duplicate alerts.

### Open Questions

- How aggressively should snapshot writes be downsampled (e.g., per mint every N seconds) once real throughput is connected?
- Should general spikes stay P3 always, or escalate to P2 when a user has a listener on the same mint?

### Next Steps

- Implement listener-aware routing so watched mints get Tier A preference and higher WS priority treatment.
- Add real websocket endpoint(s) for dashboard clients and bind Hub client lifecycle management.
- Replace PumpDev placeholder connect logic with actual upstream subscription and reconnection strategy.

## Checkpoint 2026-05-14 Listener-Aware Routing

### Current Task

Implement listener-aware routing so watched mints prefer Tier A and receive higher-priority spike delivery.

### Progress Summary

- Added in-memory listener registry (`internal/listener/registry.go`) for fast watcher lookups.
- Extended SQLite store with listener upsert/delete/list APIs and a unique `(user_id, mint)` index.
- Preloaded active listener mints from SQLite on startup to preserve behavior after restart.
- Updated spike callback routing in API bootstrap:
  - watched mint spikes now use P2 + Tier A,
  - unwatched mint spikes remain P3 + Tier B,
  - payload now includes `priority`, `tier`, and selected `rpcEndpoint`.
- Added listener management APIs:
  - `GET /api/v1/listeners/active`
  - `POST /api/v1/listeners/watch`
  - `DELETE /api/v1/listeners/watch`
- Added registry unit tests in `tests/unit/listener_registry_test.go`.
- Ran `gofmt` and `go test ./...` successfully.

### Key Decisions

- Keep routing decision path in-memory for low latency; SQLite remains source-of-truth for restart recovery.
- Use Tier A as soon as at least one user watches a mint.

### Open Questions

- Should watched-mint spikes escalate to P1 when auto-snipe is enabled for that listener?
- Should we support per-user watch scopes in websocket fanout before broad broadcast refinement?

### Next Steps

- Implement websocket connection endpoint and client registration lifecycle on Hub.
- Add per-user scoped fanout so personal events bypass global queue as P1.
- Start integrating real PumpDev websocket subscription + reconnection/backoff.

## Checkpoint 2026-05-14 WS Endpoint + Personal P1 Path

### Current Task

Implement real websocket stream endpoint and bind per-user client lifecycle into Hub with personal high-priority delivery.

### Progress Summary

- Security checked `github.com/gofiber/websocket/v2@v2.2.1` (OSV advisory count: 0) before adding dependency.
- Added websocket endpoint registration in `internal/ws/server.go`:
  - `GET /ws/stream?userId=<id>`
  - per-connection hub registration
  - queue-drain writer loop
  - heartbeat emission (`P4`)
  - graceful cleanup on disconnect.
- Extended `internal/ws/hub.go` with user-scoped client indexes and `EnqueueForUser`.
- Extended listener registry with `UsersForMint` for per-user fanout.
- Updated spike routing in `cmd/api/main.go`:
  - general spike broadcast stays `P3`
  - watched-mint personal spike fanout now emits `P1` with `Personal=true` via `EnqueueForUser`.
- Improved startup preload by loading full active listeners (`user_id + mint`) from SQLite.
- Added/updated unit tests:
  - `tests/unit/ws_hub_test.go`
  - `tests/unit/listener_registry_test.go`.
- Ran `go mod tidy`, `gofmt`, and `go test ./...` (all passing).

### Key Decisions

- Keep websocket auth lightweight for now (`userId` query param) so frontend integration can start immediately; JWT handshake hardening is next.
- Preserve queue semantics by keeping all WS delivery flowing through per-client `PriorityQueue`.

### Open Questions

- Should websocket connect require JWT immediately (`Authorization` header or signed query token)?
- Should personal fanout include additional P2 pre-spike/migration events from the same route now or after PumpDev full integration?

### Next Steps

- Add JWT-authenticated websocket handshake and map token subject to user-bound client sessions.
- Integrate real PumpDev websocket parser/reconnect and route enrichment (pump.fun + raydium logs).
- Wire frontend live feed to `/ws/stream` and render P1/P2/P3/P4 with distinct UI treatment.

## Checkpoint 2026-05-14 Telegram Auth Migration

### Current Task

Switch identity/auth to Telegram WebApp init-data verification for websocket and listener APIs.

### Progress Summary

- Added Telegram verifier in `internal/auth/telegram.go`:
  - signature verification using Telegram WebApp HMAC scheme,
  - `auth_date` freshness check,
  - user identity extraction from `user` payload.
- Updated websocket auth flow in `internal/ws/server.go`:
  - replaced open `userId` query trust,
  - now requires `tgInitData` and verifies server-side before connection registration.
- Updated HTTP listener routes in `cmd/api/main.go`:
  - `POST/DELETE /api/v1/listeners/watch` now derive `userId` from Telegram init-data,
  - `GET /api/v1/listeners/active` now returns mints for authenticated Telegram user only.
- Added store helper `ListUserListenerMints` for authenticated listener listing.
- Updated config validation (`internal/config/config.go`) to require `TELEGRAM_BOT_TOKEN` for Telegram-auth mode.
- Updated `.env.example` default to `TELEGRAM_ENABLED=true` and documented token requirement.
- Ran `gofmt` and `go test ./...` successfully.

### Key Decisions

- Treat Telegram as source-of-truth for user identity in this phase.
- Keep JWT utility code for compatibility/future expansion but no longer rely on it for listener/ws identity.

### Open Questions

- Should Telegram init-data be accepted only via header for HTTP routes (and disallow query fallback)?
- What max age should be enforced for Telegram init-data in production (`24h` currently)?

### Next Steps

- Add explicit frontend handshake utilities for generating/sending Telegram init-data to ws/http endpoints.
- Integrate real PumpDev websocket parser/reconnect and route enrichment.
- Add websocket command channel for user-specific controls (subscribe/unsubscribe from UI without reconnect).

## Checkpoint 2026-05-14 Frontend Telegram Handshake Wiring

### Current Task

Implement production-oriented frontend wiring so HTTP and websocket calls send Telegram init-data to authenticated backend routes.

### Progress Summary

- Added Telegram frontend utilities:
  - `web/src/lib/telegram.ts` (session extraction + WebApp ready call)
  - `web/src/types/telegram.d.ts` (typed Telegram WebApp globals)
- Added authenticated API client in `web/src/lib/api.ts`:
  - sends `X-Telegram-Init-Data` header for protected listener endpoints,
  - supports listener add/remove/list and recent spikes load.
- Added websocket client in `web/src/lib/ws.ts`:
  - connects to `/ws/stream` with `tgInitData` query,
  - parses live events and exposes error hook.
- Updated app shell (`web/src/app/App.tsx`) to:
  - load initial spikes + authenticated listeners,
  - open live websocket stream,
  - provide add/remove listener controls,
  - surface Telegram-auth status/warnings.
- Updated styling in `web/src/styles/global.css` for new listener controls.
- Updated `.env.example` with Vite vars (`VITE_API_BASE_URL`, `VITE_TELEGRAM_INIT_DATA`) and usage notes.
- Built frontend successfully with `npm run build`.

### Key Decisions

- Keep Telegram as sole auth path; no frontend dev bypass was introduced.
- Allow env fallback (`VITE_TELEGRAM_INIT_DATA`) for controlled local testing only.

### Open Questions

- Should `VITE_TELEGRAM_INIT_DATA` be disallowed entirely when `APP_ENV=production` to prevent misuse?
- Should listener management move from simple mint list to richer cards including sniping config state next?

### Next Steps

- Wire frontend websocket reconnect/backoff strategy and connection-state UI.
- Implement real PumpDev parser/reconnect path in backend.
- Add authenticated settings panel for Telegram user profile + notification preferences.

## Checkpoint 2026-05-14 WS Reconnect + JWT Removal

### Current Task

Add frontend websocket reconnect/backoff and confirm Telegram-only auth by removing remaining JWT runtime/dependency usage.

### Progress Summary

- Implemented resilient websocket client in `web/src/lib/ws.ts`:
  - auto reconnect with exponential backoff (capped),
  - explicit socket status callbacks (`connecting`, `connected`, `reconnecting`, `error`, `closed`),
  - clean shutdown controller.
- Updated dashboard UI (`web/src/app/App.tsx`) to display live socket status and use the new socket controller.
- Added status-pill styling in `web/src/styles/global.css`.
- Removed JWT runtime path entirely:
  - removed `jwtManager` usage from `cmd/api/main.go`,
  - bootstrap now reports `authMode=telegram`.
- Removed JWT config/secret usage:
  - removed `SecurityConfig` from `internal/config/config.go`,
  - removed `JWT_SECRET` from `.env.example`.
- Deleted legacy JWT implementation file `internal/auth/jwt.go`.
- Ran `go mod tidy`, `go test ./...`, and frontend `npm run build` successfully.

### Key Decisions

- Keep Telegram as the single authentication mechanism for user-bound routes and websocket sessions.
- Keep frontend env fallback for Telegram init-data as local testing aid only.

### Open Questions

- Should we hard-fail app startup if `VITE_TELEGRAM_INIT_DATA` is set in production frontend builds?
- Should backend reject `tgInitData` query on HTTP routes and allow header-only for stricter handling?

### Next Steps

- Implement PumpDev real websocket parsing + reconnect/backoff on backend source client.
- Add frontend connection diagnostics panel (latency, reconnect attempts, last message time).
- Continue toward end-to-end production hardening (Docker/README/final deployment flow).

## Checkpoint 2026-05-14 Mock Stream Policy Update

### Current Task

Address concern about synthetic stream usage by making mock stream behavior explicit instead of automatic.

### Progress Summary

- Updated config to support `PUMPDEV_MOCK_STREAM` in `internal/config/config.go`.
- Changed startup wiring in `cmd/api/main.go` so mock events are enabled only when that flag is true.
- Updated `.env.example` to include `PUMPDEV_MOCK_STREAM=false` with explanatory comment.
- Ran `gofmt` and `go test ./...` successfully.

### Key Decisions

- Default behavior is now realism-first: no synthetic events unless explicitly opted in.

### Open Questions

- None for mock policy; remaining work is real PumpDev parser/reconnect implementation.

### Next Steps

- Implement full PumpDev upstream websocket parsing and reconnect strategy.
- Keep `PUMPDEV_MOCK_STREAM` for deterministic local test runs and demos only.

## Checkpoint 2026-05-14 Real PumpDev Stream Integration

### Current Task

Replace PumpDev skeleton stream with real websocket consumption, tolerant payload parsing, and reconnect/backoff.

### Progress Summary

- Replaced placeholder heartbeat-only `Connect` logic in `internal/pumpdev/client.go` with:
  - real websocket dialing to configured PumpDev URL,
  - best-effort subscription messages,
  - read loop with ping/pong keepalive,
  - reconnect with exponential backoff,
  - non-blocking error reporting.
- Added robust JSON normalization path to map varied upstream payload shapes into internal `Event`:
  - supports top-level and nested payload (`data`, `event`, `result`),
  - flexible field matching for mint/symbol/volume/wallet/program/timestamp,
  - timestamp normalization for unix seconds/milliseconds and RFC3339.
- Kept `MockStream` function for explicit fallback testing only (already gated by `PUMPDEV_MOCK_STREAM`).
- Ran `gofmt`, `go mod tidy`, and `go test ./...` successfully.

### Key Decisions

- Use tolerant parser strategy to avoid hard-coupling to one message schema while upstream format is still evolving.
- Keep reconnect loop in client layer so downstream volume/listener/ws systems remain stateless about stream lifecycle.

### Open Questions

- Exact authoritative PumpDev subscription verb/shape should be confirmed against latest provider docs to reduce noise.

### Next Steps

- Validate stream against live PumpDev payload samples and tighten parser keys accordingly.
- Add metrics counters (messages parsed, dropped, reconnect count) for observability.
- Continue production hardening (Docker/runbook/final deploy docs).

## Checkpoint 2026-05-14 Docker Secret-File Support

### Current Task

Provide Docker live-test commands with safer secret handling than plain env values.

### Progress Summary

- Added `_FILE` secret loading support in `internal/config/config.go`.
  - `TELEGRAM_BOT_TOKEN_FILE`
  - `JITO_AUTH_KEY_FILE`
- Updated `.env.example` to document the new `_FILE` variables.
- Verified backend compiles and tests pass with `go test ./...`.

### Key Decisions

- Prefer mounted secret files over raw secret env values for local Docker testing.

### Open Questions

- Should we also add `JITO_BLOCK_ENGINE_URL_FILE` support for symmetry (currently not sensitive in most setups)?

### Next Steps

- Provide/run Docker live-test commands using mounted `/run/secrets/*` paths.
- Optionally add first-party Dockerfile/docker-compose for repeatable local launch.

## Checkpoint 2026-05-14 Docker Assets Added

### Completed

- Added API image build at `docker/Dockerfile.api` (multi-stage Go build, non-root runtime).
- Added local orchestration at `docker-compose.yml` with Docker `secrets:` mapped to:
  - `/run/secrets/telegram_bot_token`
  - `/run/secrets/jito_auth_key`
- Wired container env to `_FILE` variables:
  - `TELEGRAM_BOT_TOKEN_FILE`
  - `JITO_AUTH_KEY_FILE`
- Added `.dockerignore` to avoid leaking `.env`/`.secrets` and speed builds.
- Updated `.gitignore` to keep `.secrets/` ignored while allowing `docker-compose.yml` to be tracked.

### Validation

- `docker compose config` passes.
- `go test ./...` passes.

## Checkpoint 2026-05-15 Telegram Delivery + Dev Auth Fixes

### Root Cause Found

- `TELEGRAM_DEFAULT_CHAT_ID_FILE` was configured in compose but app config read only `TELEGRAM_DEFAULT_CHAT_ID`, so default chat id stayed empty.
- `telegram_default_chat_id` secret existed in compose top-level but was not mounted into API service `secrets:` list.

### Fixes Applied

- `internal/config/config.go`
  - `DefaultChatID` now uses `getEnvSecret("TELEGRAM_DEFAULT_CHAT_ID", "")`.
  - Added `TELEGRAM_DEV_USER_ID` config for local non-Telegram browser auth fallback.
- `docker-compose.yml`
  - API now mounts `telegram_default_chat_id` secret.
  - Added `TELEGRAM_DEV_USER_ID` env pass-through.
  - Added web build arg `VITE_DEV_TELEGRAM_USER_ID`.
- `web` auth fallback
  - Added `VITE_DEV_TELEGRAM_USER_ID` path in `web/src/lib/telegram.ts`.
  - API calls use `X-Dev-Telegram-User` when init-data is unavailable.
  - WS uses `devUserId` query when init-data is unavailable.
- `cmd/api/main.go` + `internal/ws/server.go`
  - Added secure dev-only fallback auth checks (disabled in production, must match configured `TELEGRAM_DEV_USER_ID`).
- Added diagnostics endpoint `POST /api/v1/notifications/test` to verify Telegram delivery without waiting for spike events.

### Validation

- `go test ./...` passes.
- `docker compose config` passes.
- `web` build passes.

## Checkpoint 2026-05-15 Telegram Auth Real-Flow Cleanup

### Current Task

Verify Telegram auth flow and remove mock/dev-auth runtime paths while continuing to record progress in `Memory.md`.

### Findings

- Telegram auth is implemented server-side through `internal/auth/telegram.go` using Telegram WebApp init-data HMAC verification, `auth_date` freshness, and user payload extraction.
- Protected listener and notification routes derive identity from Telegram init-data via `X-Telegram-Init-Data` or `tgInitData`.
- Websocket `/ws/stream` uses Telegram init-data to bind a verified Telegram user to the hub session.

### Fixes Applied

- Removed the local dev Telegram user bypass:
  - deleted `TELEGRAM_DEV_USER_ID` config,
  - removed `X-Dev-Telegram-User`, `devUserId`, and `__dev__:` handling,
  - frontend now exposes only `initData` or missing-session state.
- Removed PumpDev mock stream implementation:
  - deleted `PUMPDEV_MOCK_STREAM` config and compose/env references,
  - deleted `MockStream` from `internal/pumpdev/client.go`,
  - API now consumes only the real PumpDev websocket client.
- Removed frontend mock-data warnings and dev-user setup guidance so the UI reflects the production Telegram-only flow.
- Tightened PumpDev websocket consume loop so the ping goroutine stops on read failures and reconnect handling cannot block waiting for the outer app context.

### Key Decisions

- Keep `VITE_TELEGRAM_INIT_DATA` as a signed-init-data local testing path only; this still requires a valid Telegram signature and is not mock identity.
- Keep `/ws/public` for unauthenticated global live feed viewing; personal listener actions remain Telegram-authenticated.

### Validation

- `go test ./...` passes after rerun with access to the Go build cache.
- `docker compose config` passes.
- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Telegram Mini App Launch Wiring

### Current Task

Address missing real user authentication path through Telegram, not just backend verification of already-present init-data.

### Findings

- Backend Telegram init-data verification existed, but the frontend HTML did not load Telegram's WebApp bridge script.
- The API had no deployment-time wiring to register the app URL with the Telegram bot, so normal users had no first-party bot button/menu path that would produce signed `initData`.

### Fixes Applied

- Added Telegram WebApp bridge script to `web/index.html` so `window.Telegram.WebApp.initData` is available when launched inside Telegram.
- Expanded Telegram frontend typing and readiness handling to call `ready()` and `expand()` on the WebApp bridge.
- Added `TELEGRAM_WEB_APP_URL` and `TELEGRAM_WEB_APP_BUTTON_TEXT` config.
- Added production validation requiring `TELEGRAM_WEB_APP_URL` to be HTTPS.
- Added Telegram Bot API wiring through `ConfigureWebAppMenu`, which calls `setChatMenuButton` with the configured Mini App URL.
- Wired API startup to configure the Telegram bot menu button when `TELEGRAM_WEB_APP_URL` is set.
- Added Docker/env example entries for the Telegram Mini App URL and menu button text.
- Added bootstrap metadata flag `telegramWebApp` so runtime checks can confirm whether the Mini App launch URL is configured.

### Key Decisions

- Use Telegram Mini App launch via bot menu button as the real authentication entrypoint.
- Keep signed `VITE_TELEGRAM_INIT_DATA` only as a local testing aid; real users authenticate by opening the configured bot WebApp.

### Validation

- `go test ./...` passes.
- `docker compose config` passes.
- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Local Startup Script

### Current Task

Make local non-Docker Telegram Mini App development runnable with a simple startup command.

### Fixes Applied

- Reworked `startup.sh` to run the local stack directly instead of Docker by default.
- Script now:
  - loads `.env` safely without shell-sourcing dotenv values with spaces,
  - creates `.env` from `.env.example` if missing,
  - reads Telegram/Jito secrets from env or `.secrets/*`,
  - starts an `ngrok` HTTPS tunnel automatically when `TELEGRAM_WEB_APP_URL` is not set,
  - exports the discovered HTTPS URL before starting the API so the bot menu button can be registered,
  - starts Vite frontend and Go API as child processes,
  - cleans up child processes on Ctrl+C.
- Added Vite dev proxy for `/api`, `/healthz`, `/readyz`, and `/ws` so Telegram can reach the local API through the same HTTPS frontend tunnel.
- Marked `startup.sh` executable.

### Key Decisions

- Use the frontend tunnel as the public Telegram Mini App URL and proxy backend calls through Vite in local development.
- Keep Docker compose available separately; `startup.sh` now optimizes for the requested no-Docker workflow.

### Validation

- `bash -n startup.sh` passes.
- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Compose Local Env Default Fix

### Current Task

Resolve local Docker startup failure caused by production-only Telegram Mini App URL validation.

### Finding

- Docker Compose logs showed `TELEGRAM_WEB_APP_URL is required in production` because `docker-compose.yml` defaulted `APP_ENV` to `production` while local runs often leave `TELEGRAM_WEB_APP_URL` blank.

### Fix Applied

- Changed `docker-compose.yml` default `APP_ENV` from `production` to `development`.
- Production remains strict when `APP_ENV=production` is explicitly set.

### Validation

- `docker compose config` passes and now renders `APP_ENV: development` by default.
- `bash -n startup.sh` passes.

## Checkpoint 2026-05-15 Scripts Startup Root Fix

### Current Task

Fix `./scripts/startup.sh` after moving the startup script under the `scripts/` directory.

### Finding

- The script treated its own directory as the project root, so it looked for `scripts/web` and `scripts/.secrets` instead of root-level `web` and `.secrets`.

### Fix Applied

- Updated `scripts/startup.sh` to resolve `SCRIPT_DIR` first and set `ROOT_DIR` to its parent directory.
- Quieted harmless early ngrok API polling errors while ngrok is still starting.
- Updated the ngrok install hint to use `./scripts/startup.sh`.

### Validation

- `bash -n scripts/startup.sh` passes.
- `scripts/startup.sh` is executable.

## Checkpoint 2026-05-15 Vite Ngrok Host Allowlist

### Current Task

Fix Vite blocking generated ngrok hostnames during local Telegram Mini App testing.

### Finding

- Vite rejects unknown Host headers by default, and ngrok generates a new hostname such as `*.ngrok-free.dev`.
- Without an allowlist pattern, each regenerated ngrok URL would require a manual `vite.config` edit.

### Fix Applied

- Added Vite `server.allowedHosts` entries for ngrok tunnel domains:
  - `.ngrok-free.dev`
  - `.ngrok-free.app`
  - `.ngrok.app`
  - `.ngrok.io`

### Validation

- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Confidence + UI + DB Reset Kickoff

### Current Task

Start implementation for precision-first confidence improvements, fix Spike Tape header wrapping, and reset SQLite for a clean baseline.

### Fixes Applied

- Fixed Spike Tape header wrapping in `web/src/styles/global.css`:
  - aligned grid to all 11 rendered columns,
  - enabled horizontal overflow on tape container,
  - added no-wrap behavior for header labels,
  - increased minimum table width to prevent two-line header breaks.
- Performed full SQLite fresh reset:
  - backed up previous DB as `data/solwhisperer.db.backup.<timestamp>`,
  - deleted `data/solwhisperer.db` so schema reboots fresh on next API start.
- Applied first tracker math corrections in `internal/volume/tracker.go`:
  - fixed entry-score composition to avoid premature saturation,
  - moved depth signal to unique-buyer counting over rolling events,
  - removed float-to-int truncation in wallet concentration math,
  - replaced sticky support-floor dependency with rolling non-zero quantile support,
  - hardened ratio impulse normalization for edge thresholds.
- Added new tracker tests in `tests/unit/volume_tracker_test.go`:
  - entry score scaling with ratio,
  - unique-buyer depth behavior versus repeated single-wallet flow.

### Validation

- Targeted unit tests pass:
  - `tests/unit/volume_tracker_test.go`
  - `tests/unit/volume_processor_test.go`
- Full backend tests pass:
  - `go test ./...`
- Frontend build passes:
  - `npm run build` in `web/`

### Next Steps

- Add robust anomaly features (MAD z-score, EWMA residual, CUSUM burst) for precision-first 2-5m confidence.
- Add calibration/shadow comparison path so legacy and new confidence can run side-by-side before cutover.
- Extend persistence with feature snapshots needed for replay/backtest validation.
- `bash -n scripts/startup.sh` passes.

## Checkpoint 2026-05-15 Frontend Console Redesign + Wiring Audit

### Current Task

Replace the scaffold-like frontend with a more professional trading/monitoring console and audit websocket plus Telegram alert wiring.

### Research Notes

- Telegram Mini Apps should be mobile-first, responsive, theme-aware, and respect safe areas.
- Dashboard design should prioritize scannability, visual hierarchy, consistency, and action-oriented metrics.
- Vite `allowedHosts` should use explicit trusted host patterns instead of `true`.

### Findings

- Websocket-to-web wiring exists:
  - frontend connects to `/ws/stream` with Telegram init-data when authenticated,
  - frontend falls back to `/ws/public` for public market feed,
  - backend broadcasts `volume_spike` globally and `personal_listener_spike` to watched users.
- Telegram alerts exist but are not per-user yet:
  - spike callback sends Telegram text through `telegramNotifier.Send`,
  - watched mints raise notification priority,
  - delivery currently targets `TELEGRAM_DEFAULT_CHAT_ID` unless a specific chat ID is supplied by caller.

### Fixes Applied

- Rebuilt `web/src/app/App.tsx` as a dense signal desk:
  - top status bar,
  - watch controls,
  - live spike tape,
  - route health,
  - signal leaders,
  - alert-path visibility,
  - risk/session panels.
- Replaced `web/src/styles/global.css` with restrained terminal-style styling:
  - neutral dark palette,
  - compact table layout,
  - priority color accents,
  - Telegram safe-area padding,
  - responsive single-column mobile layout.
- Removed old hero/scaffold visuals and theme emoji controls.

### Remaining Work

- Add per-user Telegram chat binding so watched-mint alerts can be sent to the authenticated user's Telegram chat, not only the default chat.
- Run a live PumpDev payload test to confirm upstream event schema matches the tolerant parser.

### Validation

- `npm run build` in `web/` passes.
- `go test ./...` passes.

## Checkpoint 2026-05-15 Live Feed + Telegram Alert Debug

### Current Task

Investigate frontend websocket error, repeated four historical items, and missing Telegram alerts.

### Findings

- Local API and Vite were running, and `/api/v1/bootstrap` showed websocket clients connected.
- Direct websocket checks to `/ws/public` worked locally and through the ngrok URL.
- The four repeated frontend items are persisted old synthetic spike records in local SQLite:
  - two `So11111111111111111111111111111111111111112` rows,
  - two `DezXAZ8z7PnrnRJjz3wXBoRgixCa6v3wJQW9u69QyDmg` rows,
  - all with ratio `12.0` from the old mock stream period.
- Current PumpDev/PumpPortal websocket protocol uses `subscribeNewToken` and `subscribeTokenTrade`; the client was sending generic subscription shapes that PumpDev acknowledges poorly/ignores for data.
- Live PumpDev smoke test confirmed `wss://pumpdev.io/ws` sends events after `{"method":"subscribeNewToken"}`.

### Fixes Applied

- Updated `internal/pumpdev/client.go` to:
  - send `subscribeNewToken` on connect,
  - detect `txType=create`,
  - subscribe to `subscribeTokenTrade` for newly created mints on the same websocket,
  - parse PumpDev fields such as `solAmount`, `traderPublicKey`, and `txType`.
- Updated watched-mint Telegram alert delivery:
  - personal watched-mint spikes now send Telegram messages to the verified Telegram user ID,
  - unwatched spikes still use `TELEGRAM_DEFAULT_CHAT_ID`.
- Improved `scripts/startup.sh`:
  - writes API logs to `tmp/api.log`,
  - writes Vite logs to `tmp/web.log`,
  - waits for both servers to become reachable before printing ready output,
  - prints startup logs when a child process exits early.

### Remaining Notes

- Per-user Telegram sends require the Telegram user to have started the bot, otherwise Telegram can reject direct messages.
- Existing old mock rows are still in the local SQLite DB until manually cleared.

### Validation

- Direct local websocket test to `/ws/public` receives the connected event.
- Ngrok websocket test to `/ws/public` receives the connected event.
- Live PumpDev smoke test receives `connected`, `subscribed`, and `txType=create` events.
- `go test ./...` passes.
- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Floor Estimation Research And Buy-Signal Integration Plan

### Current Task

Research robust ways to estimate meme-token floor levels and define high-quality buy signals that can be integrated into the existing buy-only spike pipeline.

### Research Summary: Floor Estimation

- Do not use a single floor definition. Use a composite floor with confidence bands.
- Recommended floor models:
  - **Statistical support floor**:
    - floor = rolling lower quantile of market cap (or price) over a recent window.
    - for fast meme markets, use `q20` over the last `N=120` buy-trade snapshots (or 30-60 minutes).
  - **Liquidity-aware floor**:
    - floor should rise with depth and holder distribution quality.
    - penalize floor confidence when liquidity is thin or wallet concentration is high.
  - **Flow-derived floor**:
    - floor confidence increases when repeated buy-defense occurs near the same zone.
    - count repeated bounces near floor (`within +/- 3%`) with higher low formation.
  - **Curve-based floor (Pump-style tokens)**:
    - when bonding-curve reserves are available, derive implied spot and minimum slippage-safe level from reserve ratio and expected trade size.
    - this is more robust than raw last trade in low-liquidity phases.

### Floor Formula (Recommended Composite)

- Compute normalized sub-scores in `[0,1]`:
  - `S_support`: percentile distance above rolling support (`q20`).
  - `S_flow`: buy defense near support (bounce count + higher lows).
  - `S_depth`: depth/liquidity adequacy proxy.
  - `S_dist`: wallet-distribution quality proxy.
- Composite floor confidence:
  - `FloorConfidence = 0.35*S_support + 0.30*S_flow + 0.20*S_depth + 0.15*S_dist`
- Trade only when `FloorConfidence >= 0.65`.

### Research Summary: Buy Signals (Entry Quality)

- Existing ratio signal is necessary but not sufficient.
- Recommended signal families:
  - **Impulse strength**: buy spike ratio, buy-volume acceleration, unique buyer growth.
  - **Absorption quality**: low pullback after impulse, quick recovery after dips.
  - **Participation quality**: more unique buyers, lower top-wallet concentration.
  - **Sustainability**: signal persistence across multiple 1-2 minute slices, not a single burst.
  - **Execution risk controls**: spread/slippage, route latency, failed tx/revert ratio.

### Entry Score Formula (Buy-Only)

- Proposed weighted score in `[0,100]`:
  - `EntryScore = 30*R + 20*U + 15*P + 15*F + 10*L + 10*S`
  - `R`: normalized buy-spike ratio (saturating above threshold)
  - `U`: unique-buyer quality (growth and diversity)
  - `P`: persistence score (multi-slice confirmation)
  - `F`: floor confidence from composite floor model
  - `L`: liquidity/depth score
  - `S`: structure score (higher lows, reclaim quality)
- Suggested trigger policy:
  - watchlist candidate: `EntryScore >= 65`
  - aggressive entry: `EntryScore >= 75` and `F >= 0.70`
  - avoid chase: reject if impulse age > configured cutoff or slippage-risk exceeds threshold

### Anti-False-Positive Rules

- Reject isolated one-candle spikes with poor follow-through.
- Reject high ratio with low unique-buyer count (single-wallet dominated flow).
- Reject signals where post-spike retrace breaches dynamic floor by > configured percentage.
- Decay score rapidly when buy pressure collapses across consecutive short windows.

### Integration Plan For Current Codebase

- Phase 1 (quick wins):
  - Keep buy-only ratio logic.
  - Add `EntryScore` and `FloorConfidence` fields to websocket payload and persisted spikes.
  - Add frontend columns/chips for score bands (`A/B/C`) and floor confidence.
- Phase 2 (data enrichment):
  - Extend PumpDev normalization for reserve/depth-related fields when available.
  - Persist additional per-spike diagnostics (slice persistence, retrace %, concentration proxy).
- Phase 3 (decision layer):
  - Add score-based listener mode (`watch`, `alert-only`, `auto-ready`).
  - Gate optional auto-snipe behind score + floor-confidence + risk checks.

### Suggested Runtime Defaults

- buy ratio trigger remains strict (`ratio > 12`) as base gate.
- `unique wallets >= 5` as organic floor.
- initial floor window: `30m` rolling, recalculated every event.
- initial score thresholds:
  - candidate `65`,
  - high-confidence `75`.

### Notes

- Floor is probabilistic, not absolute. Treat it as confidence-weighted support.
- Meme regimes shift quickly; thresholds should be adaptive and re-estimated from rolling hit-rate feedback.

## Checkpoint 2026-05-15 Deep Integration Blueprint: Floor + Entry Signals (Low Latency, Low GC)

### Current Task

Produce a senior-level, implementation-ready design for token floor estimation and buy-entry signals that can be integrated into the existing Web API path with low latency and low garbage-collection overhead.

### Online Research Findings Used

- AMM price-impact mechanics (constant-product, reserve-ratio price, trade-size vs depth sensitivity):
  - Paradigm AMM price impact research.
- Raydium CLMM math and liquidity-step mechanics:
  - sqrt-price representation, tick-step execution, liquidity-to-amount formulas, and overflow-safe arithmetic.
- Raydium LaunchLab / bonding-curve lifecycle:
  - pre-graduation curve trading, post-graduation migration into AMM pools.
- Volume-flow signal references:
  - OBV-style cumulative directional volume as a feature class (with caution for false positives and one-off volume spikes).

### Core Design Principle

- Keep the current buy-only event stream as the base signal bus.
- Add a second-stage scoring engine that computes:
  - `floor_confidence` in `[0, 1]`
  - `entry_score` in `[0, 100]`
  - `entry_grade` (`A/B/C/Reject`)
- Emit scores in websocket and REST payloads without adding blocking RPC calls in the hot path.

### Floor Model (Composite, Streaming-Friendly)

- Inputs per mint (all rolling):
  - buy-side market-cap samples,
  - buy-side volume slices,
  - unique-buyer count and concentration proxy,
  - bounce behavior near support zone.
- Sub-scores:
  - `S_support`: distance above rolling support quantile (`q20` of mcap samples),
  - `S_flow`: repeated buy-defense near support (`within +/-3%`),
  - `S_depth`: depth/liquidity adequacy proxy from available reserve/impact surrogates,
  - `S_dist`: wallet-distribution quality (penalize concentration).
- Composite:
  - `floor_confidence = 0.35*S_support + 0.30*S_flow + 0.20*S_depth + 0.15*S_dist`
- Decision hint:
  - acceptable floor quality when `floor_confidence >= 0.65`.

### Buy Entry Model (Scoring)

- `entry_score = 30*R + 20*U + 15*P + 15*F + 10*L + 10*S`
  - `R`: normalized buy ratio impulse,
  - `U`: unique buyer growth/diversity,
  - `P`: persistence across short slices,
  - `F`: floor confidence,
  - `L`: liquidity/depth quality,
  - `S`: market-structure quality (higher-lows / reclaim behavior).
- Suggested grade mapping:
  - `A`: `entry_score >= 80` and `floor_confidence >= 0.70`
  - `B`: `entry_score >= 70` and `floor_confidence >= 0.65`
  - `C`: `entry_score >= 60`
  - otherwise `Reject`

### Anti-Noise / Anti-False-Positive Rules

- Reject single-burst spikes with no persistence in next short window.
- Reject high ratio if unique buyers are below dynamic threshold.
- Reject setups where post-impulse retrace violates support by configured tolerance.
- Add cool-down and decay when impulse collapses in consecutive slices.

### Web API Integration Plan

- Extend `volume.SpikeResult` with:
  - `FloorConfidence float64`
  - `EntryScore float64`
  - `EntryGrade string`
  - optional diagnostics: `DepthScore`, `PersistenceScore`, `ConcentrationScore`.
- Include these fields in websocket payloads:
  - `volume_spike`
  - `personal_listener_spike`
- Persist same fields in `spike_events` for `/api/v1/spikes/recent` parity.
- Add optional endpoint:
  - `GET /api/v1/signals/top?minGrade=B&limit=...`
  - returns top current candidates by `entry_score` with floor diagnostics.

### Low-Latency / Low-GC Engineering Constraints

- Hot path rules:
  - no network calls,
  - no large allocations,
  - no reflection/JSON churn inside scoring logic.
- Data structures:
  - fixed-size ring buffers per mint for rolling windows,
  - preallocated slices/maps with capped cardinality,
  - reuse scratch structs for score computation.
- Numeric handling:
  - prefer `float64` math with bounded rounding at output boundary only,
  - avoid string formatting in hot loop.
- Backpressure:
  - if mint cardinality exceeds cap, evict least-recently-seen mint state.
- Serialization:
  - compute score once; reuse in both persistence and fanout payload creation.

### GC-Safe State Layout (Recommended)

- `map[mint]*MintSignalState` where each state owns:
  - fixed ring for recent mcap/buy-volume samples,
  - fixed ring for short-window impulse snapshots,
  - counters for unique buyers and bounce events,
  - last computed score bundle.
- Keep value objects small and avoid nested maps in inner loops.
- Periodic compaction pass for stale states to bound heap growth.

### Rollout Sequence

- Phase A:
  - implement floor + entry score in memory only,
  - emit in websocket payloads,
  - no strategy execution side effects.
- Phase B:
  - persist score fields,
  - add `signals/top` endpoint and frontend chips.
- Phase C:
  - optional score-gated auto-snipe readiness mode.

### Validation Plan

- Unit tests:
  - score monotonicity under synthetic regime shifts,
  - floor confidence behavior under bounce/no-bounce scenarios,
  - concentration penalties and persistence gating.
- Replay tests:
  - feed historical event logs through processor and compare trigger precision/recall vs current ratio-only baseline.
- Runtime checks:
  - p95 processing latency per event,
  - heap/alloc delta under sustained stream,
  - fanout throughput with score fields enabled.

### Important Integration Note

- Keep ratio gate (`ratio > 12`) as coarse primary trigger and layer score/floor as secondary quality filters to avoid overfitting early.
- `bash -n scripts/startup.sh` passes.

## Checkpoint 2026-05-15 Signal Metadata Enhancements

### Current Task

Add copy-to-clipboard support to Signal Leaders and enrich streaming spike results with token age, token name, and market cap.

### Fixes Applied

- Extended normalized PumpDev events with:
  - token name,
  - symbol,
  - market cap in SOL,
  - transaction type.
- Extended volume tracker state and spike results with:
  - token name/symbol,
  - latest market cap,
  - token creation timestamp,
  - token age in seconds.
- Added those fields to global `volume_spike` and personal `personal_listener_spike` websocket payloads.
- Updated frontend websocket types to receive token metadata.
- Updated spike tape UI to show:
  - token name/symbol,
  - CA copy target,
  - token age,
  - current market cap,
  - ratio/wallets/volume/tier/time.
- Updated Signal Leaders so clicking the token name copies the CA to clipboard.

### Validation

- `go test ./...` passes.
- `npm run build` in `web/` passes.
- `bash -n scripts/startup.sh` passes.

## Checkpoint 2026-05-15 Live Payload Verification And Spike History Metadata

### Current Task

Confirm that the external PumpDev websocket really sends token name and market cap on live events, verify the app uses those fields correctly, and persist the same metadata into recent spike history.

### Fixes Applied

- Verified the upstream PumpDev websocket with a direct live probe after `subscribeNewToken`.
- Captured a real `txType=create` payload containing:
  - `name`,
  - `symbol`,
  - `marketCapSol`.
- Confirmed the normalized PumpDev client already maps those upstream keys into internal event fields.
- Confirmed the API websocket broadcaster already forwards those fields to frontend live clients as:
  - `name`,
  - `symbol`,
  - `marketCapSOL`.
- Confirmed the frontend live feed already maps and renders those websocket fields correctly for live rows.
- Extended persisted `spike_events` records to store:
  - `name`,
  - `symbol`,
  - `market_cap_sol`,
  - `token_created_at`,
  - `token_age_seconds`.
- Added additive SQLite migration logic so existing local databases gain the new metadata columns on restart.
- Updated spike persistence in the volume processor so emitted spikes save the same metadata used by live websocket payloads.
- Updated `/api/v1/spikes/recent` responses to return the new metadata fields.
- Updated the frontend bootstrap/history mapping so recent spike rows can show token name, symbol, market cap, and token age for newly persisted spikes.

### Important Notes

- Live websocket rows can show token name and market cap immediately when PumpDev includes them in upstream events.
- Older historical rows in SQLite still remain blank for those fields because the metadata was not stored before this checkpoint.
- Restarting the backend is required so the SQLite metadata-column migration runs and new spike records persist the added fields.

### Validation

- Direct PumpDev websocket probe returned:
  - `type=connected`,
  - `type=subscribed`,
  - a real `txType=create` event containing `name`, `symbol`, and `marketCapSol`.
- `go test ./tests/unit -run TestProcessor_PersistsSnapshotsAndEmitsSpike` passes.
- `go test ./...` passes.
- `npm run build` in `web/` passes.

## Checkpoint 2026-05-15 Phase A: Floor & Entry Score Computation (WebSocket Inline)

### Current Task

Implement floor confidence and entry score computation in the spike detector's hot path with minimal GC overhead. Emit scores inline with websocket payloads for real-time availability.

### Architecture Decision

**Delivery Method:** WebSocket inline (not REST deferred)

- Scores computed once in `Tracker.Evaluate()` during spike detection
- Emitted with spike payload to websocket hub
- Persisted to SQLite alongside spike metadata
- Frontend displays scores in live tape without secondary queries
- Rationale: Matches low-latency pattern, uses existing emit infrastructure, no GC spikes from deferred computation

### Implementation Summary

**Backend Changes:**

1. **Tracker Enhancement** (`internal/volume/tracker.go`)
   - Extended `SpikeResult` struct with `FloorConfidence`, `EntryScore`, `EntryGrade` fields
   - Extended `mintState` with rolling buffers:
     - `mcapSamples[32]`: Ring buffer for recent market cap samples (low GC)
     - `mcapIdx`: Ring buffer write pointer
     - `buoyerCount`: Unique buyer accumulator
     - `lastSupportMcap`: Rolling floor support level
     - `lastTouchAt`: Support level touchpoint timestamp
   - Added `computeFloorConfidence()`: Composite floor model (35% support + 30% flow + 20% depth + 15% distribution)
   - Added `computeEntryScore()`: Weighted signal grade (30% ratio + 20% unique + 15% persistence + 15% floor + 10% liquidity + 10% structure)
   - Added 12 helper scoring functions for sub-score computation
   - Added `gradeEntry()`: Maps entry_score + floor_confidence to letter grades (A/B/C/Reject)

2. **Storage Layer** (`internal/store/sqlite.go`)
   - Extended `SpikeEventRecord` struct with `FloorConfidence`, `EntryScore`, `EntryGrade` fields
   - Added ALTERs for spike_events table:
     - `floor_confidence REAL NOT NULL DEFAULT 0`
     - `entry_score REAL NOT NULL DEFAULT 0`
     - `entry_grade TEXT DEFAULT 'Reject'`
   - Updated `InsertSpikeEvent()` to write all three score fields
   - Updated `GetRecentSpikeEvents()` to read scores with COALESCE fallbacks

3. **Processor** (`internal/volume/processor.go`)
   - Updated `SpikeEventRecord` construction to include:
     - `FloorConfidence: result.FloorConfidence`
     - `EntryScore: result.EntryScore`
     - `EntryGrade: result.EntryGrade`

4. **API Payloads** (`cmd/api/main.go`)
   - Extended `volume_spike` websocket payload with:
     - `"floorConfidence": spike.FloorConfidence`
     - `"entryScore": spike.EntryScore`
     - `"entryGrade": spike.EntryGrade`
   - Extended `personal_listener_spike` payload with same fields

**Frontend Changes:**

1. **Types** (`web/src/lib/api.ts`, `web/src/lib/ws.ts`)
   - Added to `SpikeEvent`: `floorConfidence?: number`, `entryScore?: number`, `entryGrade?: string`
   - Added to `LiveEvent`: same three fields

2. **App Component** (`web/src/app/App.tsx`)
   - Extended `FeedItem` type with `floorConfidence?`, `entryGrade?`
   - Added formatter functions:
     - `formatGrade(grade?: string)`: Maps A/B/C/Reject to display labels
     - `formatFloorConfidence(confidence?: number)`: Renders as percentage (0-100%)
   - Updated history bootstrap to map `floorConfidence` and `entryGrade` from API response
   - Updated websocket consumer to extract both scores from live events
   - Extended tape table headers: added "Grade" and "Floor" columns
   - Extended tape rows: added grade badge (color-coded class) and floor% display

3. **Build & Compilation**
   - `go build ./cmd/api` passes
   - `go test ./tests/unit` passes (all 13 tests)
   - `npm run build` in `web/` passes
   - No TypeScript errors

### Score Formulas (Implemented)

**Floor Confidence** (0-1 range):

```
floor_confidence = 0.35*S_support + 0.30*S_flow + 0.20*S_depth + 0.15*S_dist
```

Where:

- `S_support` = normalized distance above rolling minimum mcap [0-1]
- `S_flow` = bounce resilience in ±3% band near support [0-1]
- `S_depth` = unique buyer diversity as liquidity proxy [0-1]
- `S_dist` = wallet concentration penalty [0.2 if top>40%, else 0.8]

**Entry Score** (0-100 range):

```
entry_score = 30*R + 20*U + 15*P + 15*F + 10*L + 10*S
```

Where:

- `R` = normalized ratio impulse [0-1] → [0-30]
- `U` = unique wallet growth [5-50 wallets] → [0-20]
- `P` = persistence (window_vol ≥ baseline\*1.5) → [0-15]
- `F` = floor_confidence remapped → [0-15]
- `L` = depth score → [0-10]
- `S` = market structure (mcap % of max) → [0-10]

**Grade Mapping**:

- **A**: entry_score ≥ 80 AND floor_confidence ≥ 0.70
- **B**: entry_score ≥ 70 AND floor_confidence ≥ 0.65
- **C**: entry_score ≥ 60
- **Reject**: below C threshold

### Low-GC Optimizations

- Ring buffer for mcap samples: fixed [32]float64, circular pointer (no slice allocations)
- All scoring sub-functions use local variables (no dynamic allocations on hot path)
- No network I/O, goroutines, or channel operations inside Evaluate()
- Scores computed inline before spike persistence (one memory layout)

### Testing & Validation

- Unit test `TestProcessor_PersistsSnapshotsAndEmitsSpike` validates field persistence ✓
- Unit test `TestTracker_DetectsSpikeWithOrganicWallets` validates threshold logic ✓
- All existing tests pass (13/13) ✓
- Backend compilation successful ✓
- Frontend TypeScript compilation successful ✓
- No regressions in baseline spike detection or persistence ✓

### Files Modified

1. `internal/volume/tracker.go`: SpikeResult struct, mintState buffers, score functions (12 helpers + 2 main)
2. `internal/store/sqlite.go`: SpikeEventRecord struct, schema ALTERs, read/write methods
3. `internal/volume/processor.go`: SpikeEventRecord construction
4. `cmd/api/main.go`: volume_spike and personal_listener_spike payload builders
5. `web/src/lib/api.ts`: SpikeEvent type
6. `web/src/lib/ws.ts`: LiveEvent type
7. `web/src/app/App.tsx`: FeedItem type, formatters, history mapper, websocket consumer, table display

### Next Steps (Phase B)

- **Persistence & Aggregation**: Store rolling state per mint for trend analysis (e.g., grade evolution over 10 spikes)
- **Advanced Signals**: Implement optional /api/v1/signals/top endpoint for browsing top candidates by grade
- **Strategy Execution**: Integrate entry scores into sniping decision logic (e.g., require B+ grade before auto-execute)
- **Diagnostics UI**: Breakdown view showing floor sub-scores and entry component weights for transparency

### Known Limitations

- Floor confidence uses market cap distance as proxy (not true liquidity depth; requires Raydium reserve integration later)
- Entry score does not incorporate on-chain volume profile or longer-term trend data (fixable with extended history buffers)
- Grade assignment uses simple thresholds; advanced risk models (Bayesian or regression) not yet implemented
- WebSocket clients receive scores only for new spikes post-deployment; older historical rows retain "Reject" from defaults

### Rollback Plan

- All score fields are optional (? suffix in types) → frontend gracefully displays "--" if missing
- SQLite schema migration is additive (no deletes) → safe on existing DBs
- Scores not used in any routing logic yet → no impact if values incorrect during debug
