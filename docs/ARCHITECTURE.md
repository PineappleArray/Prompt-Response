# Architecture

How a request flows from a client, through the router, to a vLLM replica.

The diagrams below render natively on GitHub. Each one names the Go subsystem that owns the work so you can jump from picture to package without grepping.

## System Architecture

```mermaid
flowchart TB
    Client["Client<br/>(OpenAI-compatible)"]

    subgraph Router["Router (cmd/router)"]
        direction TB

        subgraph MW["Middleware (internal/middleware, internal/auth, internal/ratelimit)"]
            direction LR
            ReqID["Request ID"] --> Auth["Auth"] --> RL["Rate Limit"] --> TO["Timeout 30s"] --> BL["Body Limit 1MB"]
        end

        Handler["Proxy Handler<br/>(internal/proxy)"]
        Classifier["Classifier<br/>(internal/classifier)<br/>prelim vocab → Deberta"]
        Scorer["Tier-Aware Scorer<br/>(internal/scorer)"]
        RP["Reverse Proxy<br/>+ SSE Stream<br/>(internal/proxy)"]
        Circuit["Circuit Breaker<br/>(internal/circuit)"]
        Audit["Audit Ring Buffer<br/>(internal/audit)"]
        Usage["Usage Counters<br/>(internal/usage)"]
        Metrics["Prometheus /metrics<br/>(internal/metrics)"]
    end

    Redis[("Redis<br/>prefix → replica<br/>affinity_ttl 5m")]
    Poller["Health + Metrics Poller<br/>(internal/poller)<br/>scrapes vllm:gpu_cache_usage_perc"]

    subgraph Replicas["vLLM Replicas"]
        direction LR
        Small["small tier<br/>(e.g. Qwen 1.5B)"]
        Large["large tier<br/>(e.g. Qwen 7B+)"]
    end

    Client -->|"POST /v1/chat/completions"| MW
    MW --> Handler
    Handler --> Classifier
    Classifier -->|"tier + score"| Scorer
    Scorer <-->|"lookup / record prefix hash"| Redis
    Poller -.->|"queue depth, KV cache %, health"| Scorer
    Circuit -.->|"replica health"| Scorer
    Scorer -->|"selected replica"| RP
    RP -->|"forward request"| Replicas
    Replicas -.->|"SSE stream"| RP
    RP -->|"stream response"| Client
    Poller -.->|"poll /metrics"| Replicas
    RP --> Audit
    RP --> Usage
    RP --> Metrics
```

The Scorer reads two live signals on every pick: the affinity entry from Redis (which replica recently served this prefix) and the queue depth + KV cache utilization from the Poller. The Circuit Breaker excludes replicas that have crossed the error threshold. After the response streams back, the SSE interceptor records TTFT, ITL, and TPS into the Prometheus metrics on the way out.

For the scoring weights and tier-filter behavior, see [README §Scoring Formula](../README.md#scoring-formula). For the classifier signals, see [README §Classification Signals](../README.md#classification-signals).

## Request Lifecycle

```mermaid
sequenceDiagram
    participant C as Client
    participant MW as Middleware
    participant H as Handler<br/>(internal/proxy)
    participant CL as Classifier<br/>(internal/classifier)
    participant SC as Scorer<br/>(internal/scorer)
    participant R as Redis
    participant V as vLLM Replica

    C->>MW: POST /v1/chat/completions
    MW->>MW: request ID, auth, rate limit,<br/>timeout 30s, body limit 1MB
    MW->>H: ServeHTTP
    H->>H: parse OpenAI JSON,<br/>extract system + user messages
    H->>H: xxhash64(system prompt prefix)<br/>→ prefixHash
    H->>CL: Classify(req)
    CL-->>H: tier + score + signals
    H->>SC: Pick(prefixHash, tier, excluded)
    SC->>R: GET pfx:<hash>
    R-->>SC: replica id (or miss)
    SC->>SC: filter by tier, score by<br/>affinity + queue + KV pressure
    SC-->>H: selected replica
    H->>V: forward request (reverse proxy)
    V-->>H: SSE stream (first byte → TTFT)
    loop per SSE chunk
        V-->>H: chunk (records ITL)
        H-->>C: forward chunk
    end
    V-->>H: [DONE]
    H->>SC: RecordHit(prefixHash, replica)
    SC->>R: SET pfx:<hash> EX 5m
    H->>H: record audit + usage,<br/>update Prometheus metrics
```

The hot path above assumes a healthy replica. When the chosen replica fails to produce a first byte within `stream.stall_timeout`, the handler retries against a different replica — that branch is in `internal/proxy/handler.go` (the retry loop and `peekFirstByte` dead-replica detection) and is left out of this diagram for clarity.
