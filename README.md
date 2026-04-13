# Prompt-Response

**Intelligent LLM inference router** with tier-aware routing, KV-cache-pressure scoring, prefix-cache affinity, and SJF-inspired output estimation.

Routes OpenAI-compatible requests to vLLM replicas by classifying request complexity across 6 heuristic signals, then selecting the optimal replica based on a weighted composite of cache affinity, queue depth, and GPU KV cache pressure — ensuring simple queries don't waste large-model capacity and cache-pressured replicas don't receive more traffic that would destroy their prefix cache hits.

## Architecture

```
                   Client (OpenAI-compatible API)
                              │
                    POST /v1/chat/completions
                              │
                              ▼
┌──────────────────────────────────────────────────────────────────┐
│                        HTTP Middleware                           │
│          Request ID ─► Timeout (30s) ─► Body Limit (1MB)         │
│                              │                                   │
│                              ▼                                   │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │                    Proxy Handler                          │   │
│  │  Parse OpenAI JSON ─► extract system prompt + user msg    │   │
│  │  Hash system prompt (xxhash64) for cache affinity key     │   │
│  │  Count conversation turns, detect code blocks             │   │
│  │                         │                                 │   │
│  │                         ▼                                 │   │
│  │  ┌──────────────────────────────────────────┐             │   │
│  │  │          6-Signal Classifier             │             │   │
│  │  │  length · code · reasoning · complexity  │             │   │
│  │  │  conv_depth · output_length (SJF)        │             │   │
│  │  │  ────────────────────────────            │             │   │
│  │  │  score ≥ threshold → large tier          │             │   │
│  │  │  score < threshold → small tier          │             │   │
│  │  └─────────────────┬────────────────────────┘             │   │
│  │                    │ tier + score                         │   │
│  │                    ▼                                      │   │
│  │  ┌──────────────────────────────────────────┐             │   │
│  │  │          Tier-Aware Scorer               │             │   │
│  │  │  1. Filter by matching tier              │ ◄── Redis   │   │
│  │  │  2. Score: affinity + queue + KV pressure│ ◄── Poller  │   │
│  │  │  3. Fallback to any tier if no match     │             │   │
│  │  └─────────────────┬────────────────────────┘             │   │
│  │                    │ best replica                         │   │
│  │                    ▼                                      │   │
│  │         Reverse Proxy (SSE streaming)                     │   │
│  │         Stream Interceptor: TTFT + ITL + TPS              │   │
│  └───────────────────────────────────────────────────────────┘   │
│                                                                  │
│  Endpoints: /healthz  /readyz  /v1/models  /v1/router/status     │
│             /metrics (Prometheus)                                │
└──────────────────────────────────────────────────────────────────┘
                              │
             ┌────────────────┼────────────────┐
             ▼                ▼                ▼
        ┌──────────┐     ┌─────────┐      ┌─────────┐
        │  vLLM    │     │  vLLM   │      │  vLLM   │
        │ Qwen-1.5B│     │ Qwen-7B │      │ Qwen-7B │
        │  small   │     │  large  │      │  large  │
        └──────────┘     └─────────┘      └─────────┘
```

## Key Design Decisions

### Why Tier-Aware Routing
Simple factual queries ("what is 2+2") don't need a 7B parameter model. The classifier determines request complexity and routes to the appropriate tier — small models handle simple queries faster and cheaper, large models handle complex reasoning. Graceful fallback ensures requests are always served even when no tier-matched replica exists.

### Why KV Cache Pressure Scoring
The poller scrapes `vllm:gpu_cache_usage_perc` from each replica. At 90%+ utilization, vLLM begins evicting cached prefixes and preempting running requests. Cache affinity and KV pressure are complementary signals: affinity tries to reuse cached prefixes, but pressure prevents routing to a replica where the prefix would be evicted before the request arrives — routing more traffic to a pressured replica destroys the very cache hits the affinity system built.

### Why Prefix-Cache Affinity
System prompts are hashed (xxhash64) and mapped to replicas via Redis (`pfx:<hash> → replica_id`). Requests sharing the same system prompt route to the same replica, maximizing vLLM's automatic prefix cache reuse. TTL-based expiry handles replica changes gracefully.

### Why SJF-Inspired Output Estimation
Inspired by [research on Shortest-Job-First scheduling for LLM inference](https://arxiv.org/abs/2408.15792) showing up to 5.3x latency reduction, the classifier estimates relative output length using heuristic patterns. Requests matching "what is", "yes or no" are ranked short-output; "list all", "implement" are ranked long-output. Exact token prediction is infeasible — relative ranking is what matters.

## Performance

Benchmarked on Intel Xeon Platinum 8581C:

| Operation        | Throughput | Latency | Allocs |
|------------------|-----------|-----------|--------|
| Classify (simple) | 1.4M ops/s | 875 ns | 2 allocs |
| Classify (complex) | 400K ops/s | 2.9 µs | 7 allocs |
| Pick (4 replicas) | 1.8M ops/s | 662 ns | 2 allocs |
| Score (single) | 159M ops/s | 7.3 ns | 0 allocs |

The router's hot path adds sub-microsecond overhead per request. Scoring is zero-allocation.

## Scoring Formula

```
score(replica) = w_affinity × cache_hit(0|1)
               + w_queue   × max(0, 1 − queue_depth / max_queue)
               + w_kv      × max(0, 1 − kv_cache_utilization)
               + w_base    × 0.5
```

Default weights: `affinity=0.50  queue=0.25  kv_pressure=0.15  baseline=0.10`

Tier filtering happens before scoring — replicas matching the requested tier are scored first. If no tier match is healthy, fallback to the best replica of any tier.

## Classification Signals

| Signal | Weight | Description |
|--------|--------|-------------|
| Length | 0.20 | Normalized token count (saturates at 120 tokens) |
| Code | 0.30 | Code block presence + code-related keywords |
| Reasoning | 0.15 | Reasoning/analysis keyword density |
| Complexity | 0.10 | Multi-step / edge-case keywords |
| Conv Depth | 0.10 | Number of conversation turns (multi-turn = more KV cache) |
| Output Length | 0.15 | Expected output size via SJF heuristic |

All weights are configurable in `config.yaml` — no hardcoded values in the scoring path.

## Quick Start

```bash
# Run with Docker (router + Redis)
docker compose up --build

# Or run locally (requires Redis on localhost:6379)
make run

# Route a simple request (→ small tier)
curl -s http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[
    {"role":"system","content":"You are helpful"},
    {"role":"user","content":"what is 2+2"}
  ]}' | jq .

# Route a complex request (→ large tier)
curl -s http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[
    {"role":"system","content":"You are a senior engineer"},
    {"role":"user","content":"implement a distributed consensus algorithm with edge case handling"}
  ]}' | jq .

# Check available models
curl -s http://localhost:8080/v1/models | jq .

# Check router readiness + per-replica health
curl -s http://localhost:8080/readyz | jq .

# View detailed routing state
curl -s http://localhost:8080/v1/router/status | jq .

# View Prometheus metrics
curl -s http://localhost:8080/metrics | grep router_
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | Route OpenAI-compatible request to best replica |
| `/v1/models` | GET | List available models (OpenAI-compatible) |
| `/v1/router/status` | GET | Detailed routing state with per-replica metrics |
| `/healthz` | GET | Liveness probe (always 200) |
| `/readyz` | GET | Readiness probe with per-replica health JSON |
| `/metrics` | GET | Prometheus metrics |

**Error responses** follow the OpenAI error format with `request_id` for correlation:
```json
{
  "error": {
    "message": "no healthy replicas available",
    "type": "service_unavailable",
    "code": "no_replicas",
    "request_id": "a1b2c3d4e5f6g7h8"
  }
}
```

## Observability

**Prometheus metrics** at `/metrics`:
- `router_requests_total{tier, replica, cache_hit}` — Request counter
- `router_request_duration_seconds{tier, replica}` — Total latency histogram
- `router_ttft_seconds{tier, replica}` — Time-to-first-token histogram (measured at first SSE byte, not completion)
- `router_inter_token_latency_seconds{tier, replica}` — Inter-token latency histogram (time between consecutive SSE chunks with content). High ITL indicates KV cache thrashing, batch preemption, or GPU contention.
- `router_output_tokens{tier, replica}` — Output tokens per request (estimated from SSE stream content)
- `router_tokens_per_second{tier, replica}` — Output token throughput per request
- `router_classifier_score{tier}` — Complexity score distribution
- `router_replica_kv_cache_utilization{replica}` — Per-replica KV cache gauge

### SSE Stream Instrumentation

The router intercepts SSE response streams in real-time to measure three critical LLM inference metrics without modifying the data flowing to clients:

- **TTFT (Time-to-First-Token)**: Captured at the first `Write()` call — the true latency before the user sees any output.
- **ITL (Inter-Token Latency)**: Average time between consecutive SSE chunks containing token content. The key signal for detecting inference stalls caused by KV cache pressure, batch scheduling delays, or GPU contention.
- **TPS (Tokens Per Second)**: Output throughput computed from total tokens and stream duration. The metric operators use for capacity planning and SLA monitoring.

Together, TTFT + ITL + TPS form the complete inference performance trifecta — most routing layers only measure request-level latency.

**Structured JSON logging** via `log/slog`:
```json
{"time":"...","level":"INFO","msg":"completed",
 "replica":"replica-small-1","ttft_ms":45,"total_ms":1200,
 "output_tokens":150,"tokens_per_sec":125.0,"avg_itl_ms":8,
 "cache_hit":"miss"}
```

**Request ID** propagation via `X-Request-ID` header — correlate router logs with vLLM logs.

## Configuration

```yaml
listen_addr: ":8080"

replicas:
  - id: replica-small-1
    url: http://localhost:8001
    model: Qwen/Qwen2.5-1.5B-Instruct
    tier: small
  - id: replica-large-1
    url: http://localhost:8002
    model: Qwen/Qwen2.5-7B-Instruct
    tier: large

redis:
  addr: localhost:6379

# Replica scoring weights
weights:
  cache_affinity: 0.50
  queue_depth: 0.25
  kv_cache_pressure: 0.15
  baseline: 0.10

# Classifier signal weights
classifier:
  length: 0.20
  code: 0.30
  reasoning: 0.15
  complexity: 0.10
  conv_depth: 0.10
  output_length: 0.15

affinity_ttl: 5m
threshold: 0.35
max_queue: 20
poll_interval: 2s
```

All fields have sensible defaults. Minimum required: `replicas` (at least one) and `redis.addr`.

## Project Structure

```
cmd/router/main.go          Entry point, initialization, graceful shutdown
internal/
├── classifier/              6-signal heuristic classifier
├── config/                  YAML config with validation + defaults
├── metrics/                 Prometheus metric definitions
├── middleware/               Request ID, timeout, body size limit
├── poller/                  Health polling + Prometheus metrics scraping
├── proxy/                   HTTP handler, reverse proxy, SSE stream instrumentation
├── scorer/                  Tier-aware replica selection
├── store/                   Affinity cache (Redis + in-memory)
└── types/                   Shared type definitions
```

## Testing

```bash
make test     # 52 tests, race detector enabled
make bench    # Performance benchmarks
make lint     # go vet + gofmt
```

Test coverage includes:
- **Classifier**: 9 tests — tier classification, signal presence, boundary cases
- **Scorer**: 7 tests — tier matching, fallback, KV pressure, affinity, all-unhealthy
- **Poller**: 7 tests — circuit breaker, recovery, timeout, multi-replica independence
- **Handler**: 13 tests — routing, error responses, health endpoints, API compatibility, edge cases
- **Stream**: 8 tests — SSE parsing, token counting, ITL measurement, fragmented writes, flush delegation
- **Config**: 8 tests — validation, defaults, error cases

## Known Limitations

- **Heuristic classification**: Production deployments would benefit from embedding-based semantic routing. The heuristic approach is intentionally chosen for single-binary simplicity.
- **No request lifecycle timeouts**: A hung vLLM replica holds the connection until client disconnect.
- **Single router instance**: No built-in HA. Deploy behind a load balancer for redundancy.
- **No rate limiting**: No per-client throttling. Add at the load balancer layer.
- **Partial Redis resilience**: If Redis goes down, affinity degrades to queue-depth-only scoring — routing continues, cache hit rates decrease.

## Development

```bash
make build    # Build to bin/router (with version info)
make test     # Run all tests with race detector
make bench    # Run performance benchmarks
make lint     # go vet + gofmt check
make docker   # docker compose up --build
make clean    # Remove build artifacts
```

### Layer 3 — Scorer (`scorer.go`)
Checks Redis for semantically similar prompts that have already been processed. If a cache hit is found above a similarity threshold, the cached response is returned. Otherwise, it selects an available replica from the registry for the assigned tier.

### Layer 4 — Dispatcher (WIP)
Forwards the request to the selected vLLM replica. Handles connection management and response streaming back to the scorer.

### Layer 5 — Response Handler (WIP)
Receives the vLLM response, writes it to the Redis cache with a prompt hash key, and returns it to the client.

---

## Project Structure

```
Prompt-Response/
├── cmd/
│   └── router/
│       └── main.go              # HTTP server entry point
├── internal/
│   └── classifier/
│       ├── classifier.go        # Classifier interface + structs
│       ├── huristicClassifier.go # Heuristic scoring logic
│       └── huristic_test.go     # Unit tests
└── README.md
```

---

## Getting Started

### Prerequisites
- Go 1.21+
- Redis (for prompt caching)
- At least one running vLLM instance (Ollama or Docker)

### Install

```bash
git clone https://github.com/PineappleArray/Prompt-Response.git
cd Prompt-Response
go mod tidy
```

### Run Tests

```bash
go test ./internal/classifier/...
```

### Run the Router

```bash
go run cmd/router/main.go
```

---

## Configuration

The classifier weights and tier thresholds are configurable at initialization:

```go
classifier := newHeuristic(HeuristicConfig{
    Weights: SignalWeights{
        Length:     0.25,
        Code:       0.35,
        Reasoning:  0.25,
        Complexity: 0.15,
    },
    Threshold: 0.5,
})
```

---

## Known Limitations

This is a portfolio-grade project with intentional scope constraints:

| Limitation | Detail |
|---|---|
| No crash checking | If a vLLM replica crashes it will still receive signals |
| No request lifecycle | No timeouts — a hung vLLM will remain hung |
| No autoscaling | Docker testing setup prevents horizontal scaling |
| No rate limiting | No per-client resource limits |
| Partial Redis resilience | No backup store, periodic writes only |
| Single router | Router is not horizontally scaled |

---

## Roadmap

- [ ] Finish Layer 4 dispatcher
- [ ] Finish Layer 5 response handler
- [ ] Wire up `main.go` with HTTP server
- [ ] Replace heuristic classifier with lightweight ML model
- [ ] Add medium tier routing logic
- [ ] Redis connection pooling
- [ ] OpenAI-compatible `/v1/chat/completions` endpoint

---

## Tech Stack

- **Go** — proxy handler, classifier, router
- **Redis** — prompt similarity cache
- **vLLM / Ollama** — local model replicas
- **Docker** — replica containerization
