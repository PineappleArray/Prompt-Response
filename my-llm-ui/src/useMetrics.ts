import { useCallback, useEffect, useState } from "react";

// ── Types mirroring the Go router's JSON endpoints ─────────────────
export type TenantUsage = {
  input_tokens: number;
  output_tokens: number;
  requests: number;
  first_seen: string;
  last_seen: string;
};

export type UsageResponse = {
  tenants: Record<string, TenantUsage>;
  count: number;
  enabled: boolean;
};

export type ReplicaStatus = {
  provider: string;
  id: string;
  model: string;
  tier: string;
  healthy: boolean;
  // vLLM-only fields — absent for API provider replicas.
  queue_depth?: number;
  kv_cache_util?: number;
  running?: number;
  circuit: string;
  error_rate: number;
};

export type StatusResponse = {
  status: string;
  total_replicas: number;
  healthy_count: number;
  replicas: ReplicaStatus[];
};

export type TierUsage = {
  tier: string;
  model: string;
  requests: number;
  routed_cost: number;
  baseline_cost: number;
  savings: number;
  avg_ttft_ms: number;
};

export type TiersResponse = {
  tiers: TierUsage[];
  total_requests: number;
  total_savings: number;
  total_routed: number;
  total_baseline: number;
};

type Metrics = {
  usage: UsageResponse | null;
  status: StatusResponse | null;
  tiers: TiersResponse | null;
};

// fetchMetrics performs the network work only — no React state — so it can be
// shared by the mount effect and the manual refresh without either touching
// setState through a shared callback. Throws on any non-OK response.
async function fetchMetrics(signal?: AbortSignal): Promise<Metrics> {
  const [usageRes, statusRes, tiersRes] = await Promise.all([
    fetch("/v1/router/usage", { signal }),
    fetch("/v1/router/status", { signal }),
    fetch("/v1/router/tiers", { signal }),
  ]);

  if (!usageRes.ok || !statusRes.ok || !tiersRes.ok) {
    throw new Error(
      `request failed (usage ${usageRes.status}, status ${statusRes.status}, tiers ${tiersRes.status})`
    );
  }

  const [usage, status, tiers] = await Promise.all([
    usageRes.json() as Promise<UsageResponse>,
    statusRes.json() as Promise<StatusResponse>,
    tiersRes.json() as Promise<TiersResponse>,
  ]);
  return { usage, status, tiers };
}

// useMetrics fetches the usage / status / tier breakdowns from the backend
// (proxied via /v1 → :8080). Mirrors the shape of useChat: returns data plus
// loading/error state and a refresh callback.
export function useMetrics() {
  const [data, setData] = useState<Metrics>({ usage: null, status: null, tiers: null });
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  // Bumping this re-runs the load effect; the Refresh button increments it.
  const [reloadKey, setReloadKey] = useState<number>(0);

  useEffect(() => {
    const ctrl = new AbortController();
    // Async IIFE: every setState below runs after an await, so none fire
    // synchronously during the effect.
    (async () => {
      try {
        const next = await fetchMetrics(ctrl.signal);
        if (ctrl.signal.aborted) return;
        setData(next);
        setError(null);
      } catch (err: unknown) {
        if (ctrl.signal.aborted) return;
        setError(err instanceof Error ? err.message : "Failed to load metrics");
      } finally {
        if (!ctrl.signal.aborted) setLoading(false);
      }
    })();
    return () => ctrl.abort();
  }, [reloadKey]);

  const refresh = useCallback(() => {
    setLoading(true);
    setReloadKey((k) => k + 1);
  }, []);

  return { ...data, loading, error, refresh };
}
