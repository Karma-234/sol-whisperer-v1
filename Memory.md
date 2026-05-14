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
