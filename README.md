# Prompt-Response

Intelligent LLM inference router with **tier-aware routing**, **KV-cache-pressure scoring**, **prefix-cache affinity**, and **SJF-inspired output estimation**.

Routes OpenAI-compatible requests to vLLM replicas by classifying request complexity, then selecting the optimal replica based on a weighted composite of cache affinity, queue depth, and GPU KV cache pressure.

## Architecture

```
              Client (OpenAI-compatible API)
                        │
                        ▼
┌─────────────────────────────────────────────────────────┐
│                    HTTP Handler                          │
│  Parse request → extract system prompt + user message    │
│  Estimate tokens, detect code blocks, count conv turns   │
│  Hash system prompt (xxhash64) for cache affinity key    │
│                        │                                 │
│                        ▼                                 │
│              ┌─────────────────┐                         │
│              │   Classifier    │                         │
│              │  6-signal       │                         │
│              │  heuristic      │                         │
│              │  scoring        │                         │
│              └────────┬────────┘                         │
│                       │ tier + score                     │
│                       ▼                                  │
│              ┌─────────────────┐                         │
│              │     Scorer      │                         │
│              │  tier-aware     │  ◄── Redis (affinity)   │
│              │  replica        │  ◄── Poller (health)    │
│              │  selection      │                         │
│              └────────┬────────┘                         │
│                       │ best replica                     │
│                       ▼                                  │
│              Reverse Proxy (SSE streaming)                │
│              TTFT measured at first byte                  │
└─────────────────────────────────────────────────────────┘
                        │
           ┌────────────┼────────────┐
           ▼            ▼            ▼
      ┌─────────┐ ┌─────────┐ ┌─────────┐
      │ vLLM    │ │ vLLM    │ │ vLLM    │
      │ small   │ │ large   │ │ large   │
      │ tier    │ │ tier    │ │ tier    │
      └─────────┘ └─────────┘ └─────────┘
```

## Key Design Decisions

### Tier-Aware Routing
The classifier determines whether a request needs a small or large model. The scorer filters replicas by matching tier before scoring — a simple factual query won't waste a 7B model's capacity. Graceful fallback ensures requests are always served even when no tier-matched replica is available.

### KV Cache Pressure Scoring
The poller scrapes `vllm:gpu_cache_usage_perc` from each replica. The scorer penalizes replicas nearing cache exhaustion. At 90%+ utilization, vLLM begins evicting cached prefixes and preempting running requests — routing **more** traffic there destroys the very prefix cache hits the affinity system worked to build. Cache affinity and KV pressure are complementary: affinity tries to reuse cached prefixes, pressure prevents routing to a replica where the prefix would be evicted before the request arrives.

### Prefix-Cache Affinity
System prompts are hashed with xxhash64 and stored in Redis as `pfx:<hash> → replica_id` with a configurable TTL. Requests with the same system prompt route to the same replica, maximizing vLLM's automatic prefix cache reuse.

### SJF-Inspired Output Estimation
Inspired by research on Shortest-Job-First scheduling for LLM inference (which showed up to 5.3x latency reduction), the classifier estimates relative output length using heuristic patterns rather than predicting exact token counts. Requests matching "what is", "yes or no" patterns are ranked as short-output; "list all", "implement", "generate" as long-output.

### 6-Signal Heuristic Classification
Rather than a binary simple/complex split, the classifier combines six weighted signals:

| Signal | Weight | Description |
|--------|--------|-------------|
| Length | 0.20 | Normalized token count (max at 120 tokens) |
| Code | 0.30 | Code block presence + code-related keywords |
| Reasoning | 0.15 | Reasoning/analysis keywords |
| Complexity | 0.10 | Multi-step / edge-case keywords |
| Conv Depth | 0.10 | Number of conversation turns |
| Output Length | 0.15 | Expected output size (SJF heuristic) |

### Scoring Formula
```
score(replica) = w_affinity × cache_hit(0|1)
               + w_queue   × max(0, 1 - queue_depth / max_queue)
               + w_kv      × max(0, 1 - kv_cache_utilization)
               + w_base    × 0.5
```
Default weights: affinity=0.50, queue=0.25, kv_pressure=0.15, baseline=0.10

## Quick Start

```bash
# Run with Docker
docker compose up --build

# Or run locally (requires Redis on localhost:6379)
make run

# Test routing a simple request
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"what is 2+2"}]}'

# Check router readiness and replica states
curl http://localhost:8080/readyz

# View Prometheus metrics
curl http://localhost:8080/metrics
```

## Configuration

| Field | Default | Description |
|-------|---------|-------------|
| `listen_addr` | `:8080` | HTTP listen address |
| `replicas[].id` | — | Unique replica identifier |
| `replicas[].url` | — | vLLM base URL |
| `replicas[].model` | — | Model name |
| `replicas[].tier` | — | `small`, `medium`, or `large` |
| `redis.addr` | — | Redis connection address |
| `weights.cache_affinity` | `0.50` | Prefix cache affinity weight |
| `weights.queue_depth` | `0.25` | Queue depth weight |
| `weights.kv_cache_pressure` | `0.15` | GPU KV cache pressure weight |
| `weights.baseline` | `0.10` | Constant tiebreaker weight |
| `affinity_ttl` | `5m` | Redis affinity entry TTL |
| `threshold` | `0.35` | Classifier score threshold for large tier |
| `max_queue` | `20` | Queue depth normalization ceiling |

## Observability

The router exports Prometheus metrics at `/metrics`:
- `router_requests_total` — Total requests by tier, replica, cache hit
- `router_request_duration_seconds` — Total request duration histogram
- `router_ttft_seconds` — Time-to-first-token histogram (measured at first SSE byte, not completion)
- `router_classifier_score` — Classifier score distribution by tier
- `router_replica_kv_cache_utilization` — Per-replica KV cache utilization gauge

Health endpoints:
- `GET /healthz` — Liveness (always 200)
- `GET /readyz` — Readiness with per-replica status JSON

## Known Limitations

This is a proof-of-concept. Known gaps for production use:

- **No crash detection**: If a vLLM process crashes (vs. becoming unresponsive), the 3-strike health check has a ~6s detection lag
- **No request lifecycle timeouts**: A hung vLLM replica will hold the connection until the client disconnects
- **No horizontal scaling**: Single router instance is a SPOF
- **No rate limiting**: No per-client request throttling
- **Heuristic classification**: Production would benefit from embedding-based semantic routing (e.g., BERT) instead of keyword heuristics
- **Partial Redis resilience**: If Redis goes down, affinity degrades to round-robin-like scoring but the router continues serving

## Development

```bash
make build    # Build binary to bin/router
make test     # Run all tests with race detector
make lint     # go vet + gofmt check
make docker   # docker compose up --build
make clean    # Remove build artifacts
```
