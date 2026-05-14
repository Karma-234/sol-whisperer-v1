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
