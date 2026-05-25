package usage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresConfig configures the Postgres-backed usage sink.
type PostgresConfig struct {
	DSN           string
	FlushInterval time.Duration
	BatchSize     int
	BufferSize    int
}

const usageSchema = `
CREATE TABLE IF NOT EXISTS usage_totals (
    tenant        text        PRIMARY KEY,
    input_tokens  bigint      NOT NULL DEFAULT 0,
    output_tokens bigint      NOT NULL DEFAULT 0,
    requests      bigint      NOT NULL DEFAULT 0,
    first_seen    timestamptz,
    last_seen     timestamptz
)`

const upsertSQL = `
INSERT INTO usage_totals (tenant, input_tokens, output_tokens, requests, first_seen, last_seen)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant) DO UPDATE SET
    input_tokens  = usage_totals.input_tokens  + EXCLUDED.input_tokens,
    output_tokens = usage_totals.output_tokens + EXCLUDED.output_tokens,
    requests      = usage_totals.requests      + EXCLUDED.requests,
    last_seen     = GREATEST(usage_totals.last_seen, EXCLUDED.last_seen)`

// PostgresSink is a BatchSink whose flush writes batched UPSERTs into
// usage_totals. Its Close also tears down the pgx pool.
type PostgresSink struct {
	*BatchSink
	pool *pgxpool.Pool
}

// NewPostgresSink opens a pool, ensures the schema exists, and starts
// the background worker.
func NewPostgresSink(ctx context.Context, cfg PostgresConfig) (*PostgresSink, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	if _, err := pool.Exec(ctx, usageSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("creating usage_totals: %w", err)
	}

	flush := func(ctx context.Context, batch []UsageEvent) error {
		return upsertUsage(ctx, pool, batch)
	}
	return &PostgresSink{
		BatchSink: NewBatchSink(cfg.BufferSize, cfg.BatchSize, cfg.FlushInterval, flush),
		pool:      pool,
	}, nil
}

func (p *PostgresSink) Close() error {
	err := p.BatchSink.Close()
	p.pool.Close()
	return err
}

// upsertUsage collapses the batch per tenant in memory so each tenant
// produces a single row, then sends one pgx.Batch round-trip.
func upsertUsage(ctx context.Context, pool *pgxpool.Pool, events []UsageEvent) error {
	type agg struct {
		in, out, n         int64
		first, last        time.Time
	}
	tenants := make(map[string]*agg, len(events))
	for _, ev := range events {
		a, ok := tenants[ev.Tenant]
		if !ok {
			a = &agg{first: ev.At, last: ev.At}
			tenants[ev.Tenant] = a
		}
		a.in += int64(ev.In)
		a.out += int64(ev.Out)
		a.n++
		if ev.At.Before(a.first) {
			a.first = ev.At
		}
		if ev.At.After(a.last) {
			a.last = ev.At
		}
	}

	batch := &pgx.Batch{}
	for tenant, a := range tenants {
		batch.Queue(upsertSQL, tenant, a.in, a.out, a.n, a.first, a.last)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(tenants); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("usage upsert: %w", err)
		}
	}
	return nil
}
