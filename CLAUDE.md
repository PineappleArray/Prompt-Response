# Prompt-Response

Intelligent LLM inference router for vLLM replicas with tier-aware routing, KV-cache-pressure scoring, and prefix-cache affinity.

## Build & Test

```bash
make build        # Compile to bin/router
make test         # All tests with -race -count=1
make bench        # Classifier & scorer benchmarks
make lint         # go vet + gofmt check
make run          # Build and execute
make docker       # docker compose up --build
make clean        # Remove bin/
```

## Architecture

- **cmd/router/main.go** — HTTP server entry point with graceful shutdown
- **internal/classifier/** — 6-signal heuristic request complexity classification
- **internal/config/** — YAML config parsing and validation
- **internal/metrics/** — Prometheus metric definitions
- **internal/middleware/** — Request ID, timeout, body size limit
- **internal/poller/** — Health polling and metrics scraping from vLLM
- **internal/proxy/** — HTTP handler, reverse proxy, SSE stream instrumentation
- **internal/scorer/** — Tier-aware replica selection with weighted scoring
- **internal/store/** — Prefix-cache affinity (Redis + in-memory fallback)
- **internal/types/** — Shared type definitions

## Conventions

- **Go 1.26** — use current idioms, no legacy patterns
- **Zero-allocation hot paths** — classifier and scorer are perf-critical, avoid heap allocations
- **Table-driven tests** — all test files use `[]struct{ name string; ... }` pattern
- **Race detector** — tests always run with `-race`; never introduce data races
- **go vet + gofmt** — code must pass `make lint` before committing
- **Error handling** — return errors, don't panic; use `fmt.Errorf` with `%w` for wrapping
- **No global state** — pass dependencies via constructors, not package-level vars
- **Structured logging** — use `slog` for all logging with key-value pairs

## Key Design Decisions

- Tier-aware routing prevents sending simple prompts to large models
- KV cache pressure monitoring avoids replicas above 90% cache utilization
- Prefix-cache affinity uses xxhash64 of system prompt prefixes via Redis
- SJF-inspired output estimation for better request scheduling
- Prometheus metrics for TTFT, ITL, TPS, and cache utilization

## Endpoints

- `POST /v1/chat/completions` — OpenAI-compatible routing
- `GET /v1/models` — List available models
- `GET /v1/router/status` — Detailed routing metrics
- `GET /healthz` — Liveness probe
- `GET /readyz` — Readiness with per-replica health
- `GET /metrics` — Prometheus metrics

## Configuration

All tuning lives in `config.yaml`. Key knobs:
- `weights.*` — Scoring weights for replica selection (cache_affinity, queue_depth, kv_cache_pressure, baseline)
- `classifier.*` — Signal weights for complexity classification (length, code, reasoning, complexity, conv_depth, output_length)
- `threshold` — Complexity score above which requests route to large tier (0.35)
- `affinity_ttl` — How long prefix-cache affinity entries persist (5m)
- `max_queue` — Per-replica queue depth limit before shedding (20)

## Skill Routing

Available skills for this project:
- `/review` — Pre-landing code review against project standards
- `/investigate` — Systematic debugging with hypothesis testing
- `/qa` — Test quality audit and coverage analysis
- `/ship` — Release workflow: lint, test, commit, push
- `/bench` — Performance analysis and benchmark regression detection
