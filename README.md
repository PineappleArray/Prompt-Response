# Prompt-Response
# Prompt-Response

A token-aware LLM routing layer that intelligently dispatches prompts to the right model based on complexity, code presence, reasoning depth, and prompt length — maximizing output quality while minimizing token cost.

---

## What It Does

Most LLM setups send every request to the same model regardless of complexity. A one-line factual question doesn't need the same model as a multi-step distributed systems design prompt. Prompt-Response sits in front of your vLLM replicas and routes each request to the right tier automatically.

```
Client Request
      ↓
  Proxy Handler      →  parses messages, counts tokens, detects code
      ↓
  Classifier         →  scores prompt across 4 signals, assigns tier
      ↓
  Scorer             →  checks cache for similar prompts, selects replica
      ↓
  Dispatcher         →  forwards to appropriate vLLM replica
      ↓
  Response Handler   →  streams response back to client
```

---

## Architecture

### Layer 1 — Proxy Handler (`handler.go`)
Receives incoming requests and builds a structured representation:

```go
type Request struct {
    SystemPrompt string // extracted from messages[role=system]
    UserMessage  string // latest messages[role=user]
    TokenCount   int    // pre-counted by proxy handler
    HasCode      bool   // true if ``` or code keywords found
    ConvTurns    int    // number of prior messages in thread
}
```

### Layer 2 — Classifier (`heuristic.go`)
Scores the request across four weighted signals and assigns a model tier:

| Signal     | Weight | Description                                      |
|------------|--------|--------------------------------------------------|
| Length     | 0.25   | Token count normalized against 120-token baseline |
| Code       | 0.35   | Code block presence or code-related keywords     |
| Reasoning  | 0.25   | Explain/compare/design/architecture keywords     |
| Complexity | 0.15   | Multi-step, edge case, scale, production signals |

**Tiers:**

| Tier   | Score Range | Use Case                              |
|--------|-------------|---------------------------------------|
| Small  | < 0.5       | Factual Q&A, simple lookups           |
| Medium | 0.5 – 0.75  | Moderate reasoning, short code tasks  |
| Large  | > 0.75      | Complex code, architecture, deep reasoning |

The classifier returns:

```go
type Response struct {
    Tier    ModelTier          // routing decision
    Score   float64            // raw composite score 0–1
    Signals map[string]float64 // per-signal breakdown
    Reason  string             // human-readable explanation
}
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
