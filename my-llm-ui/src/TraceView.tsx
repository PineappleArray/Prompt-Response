import { useCallback, useEffect, useState } from "react";
import { palette } from "./theme";

type Signals = {
  length?: number;
  code?: number;
  reasoning?: number;
  complexity?: number;
  conv_depth?: number;
  output_length?: number;
};

type AuditRecord = {
  timestamp: string;
  request_id: string;
  tenant: string;
  tier: string;
  class_score: number;
  signals: Signals;
  replica_id: string;
  replica_tier: string;
  cache_hit: boolean;
  attempts: number;
  ttft_ms: number;
  total_ms: number;
  output_tokens: number;
  status_code: number;
  reason: string;
};

type AuditResponse = {
  records: AuditRecord[];
  count: number;
  enabled: boolean;
};

const TIER_COLOR: Record<string, string> = {
  small:     "#6b8e6b",
  code:      "#6b7e8e",
  medium:    "#8e7e6b",
  large:     "#7e6b8e",
  reasoning: "#8e6b6b",
};

function statusColor(code: number): string {
  if (code >= 200 && code < 300) return "#3f7d4f";
  if (code === 503) return "#8b3a2a";
  return "#7a6b2a";
}

function fmt(n: number, unit: string): string {
  return n > 0 ? `${n}${unit}` : "—";
}

export default function TraceView() {
  const [records, setRecords] = useState<AuditRecord[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    const ctrl = new AbortController();
    (async () => {
      try {
        const res = await fetch("/v1/router/audit?limit=50", { signal: ctrl.signal });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: AuditResponse = await res.json();
        if (!ctrl.signal.aborted) {
          setRecords((data.records ?? []).slice().reverse());
          setEnabled(data.enabled);
          setError(null);
        }
      } catch (err) {
        if (!ctrl.signal.aborted) {
          setError(err instanceof Error ? err.message : "Failed to load audit");
        }
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

  return (
    <div style={{ flex: 1, overflowY: "auto", padding: "24px 16px" }}>
      <div style={{ maxWidth: 960, width: "100%", margin: "0 auto" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
          <div>
            <h1 style={{ fontSize: 22, fontWeight: 600, letterSpacing: "-0.02em", margin: 0 }}>
              Routing Trace
            </h1>
            <p style={{ fontSize: 13, color: "var(--text-secondary)", margin: "4px 0 0" }}>
              Live execution flow — classify → select tier → pick replica → stream
            </p>
          </div>
          <button
            onClick={refresh}
            style={{ border: "1px solid var(--border-color)", background: "var(--bg-secondary)", borderRadius: 10, padding: "6px 14px", cursor: "pointer", color: "var(--text-primary)", fontFamily: "inherit", fontSize: 13, fontWeight: 550 }}
            onMouseEnter={(e) => (e.currentTarget.style.background = "var(--bg-hover)")}
            onMouseLeave={(e) => (e.currentTarget.style.background = "var(--bg-secondary)")}
          >
            Refresh
          </button>
        </div>

        {!enabled && (
          <div style={{ padding: "10px 16px", borderRadius: 12, background: "#2a2a1a", color: "#9a8a4a", fontSize: 13, marginBottom: 16 }}>
            Audit trail is disabled. Set <code>audit.enabled: true</code> in config.yaml.
          </div>
        )}

        {loading && <div style={{ color: "var(--text-muted)", padding: "40px 0", textAlign: "center" }}>Loading trace…</div>}
        {error && (
          <div style={{ padding: "10px 16px", borderRadius: 12, background: "#f5e6e1", color: "#8b3a2a", fontSize: 14, marginBottom: 16 }}>
            {error}
          </div>
        )}

        {!loading && !error && records.length === 0 && (
          <div style={{ textAlign: "center", color: "var(--text-muted)", padding: "60px 0", fontSize: 14 }}>
            No routing decisions recorded yet. Send a chat message to see the execution flow.
          </div>
        )}

        {!loading && !error && records.map((rec, i) => (
          <div
            key={rec.request_id + i}
            style={{
              background: "var(--bg-secondary)",
              border: "1px solid var(--border-color)",
              borderRadius: 14,
              padding: 16,
              marginBottom: 12,
              fontFamily: "inherit",
            }}
          >
            {/* Header row */}
            <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12, flexWrap: "wrap" }}>
              <span style={{ fontSize: 12, color: "var(--text-muted)", fontVariantNumeric: "tabular-nums" }}>
                {new Date(rec.timestamp).toLocaleTimeString()}
              </span>
              <span style={{ fontSize: 11, fontFamily: "monospace", color: "var(--text-muted)", letterSpacing: 0 }}>
                {rec.request_id.slice(0, 12)}…
              </span>
              <span style={{ fontSize: 11, fontWeight: 600, color: statusColor(rec.status_code), marginLeft: "auto" }}>
                HTTP {rec.status_code}
              </span>
            </div>

            {/* Flow steps */}
            <div style={{ display: "flex", alignItems: "stretch", gap: 0, overflowX: "auto" }}>
              {/* Step 1: Classify */}
              <FlowStep label="CLASSIFY" color="#5a7a9a">
                <Row label="tier" value={<TierBadge tier={rec.tier} />} />
                <Row label="score" value={(rec.class_score * 100).toFixed(0) + "%"} />
                <Row label="reason" value={rec.reason || "—"} small />
              </FlowStep>

              <Arrow />

              {/* Step 2: Route */}
              <FlowStep label="ROUTE" color={TIER_COLOR[rec.replica_tier] ?? "#6b6b7e"}>
                <Row label="replica" value={rec.replica_id || "—"} small />
                <Row label="tier" value={<TierBadge tier={rec.replica_tier} />} />
                <Row label="cache" value={rec.cache_hit ? "hit ✓" : "miss"} />
                {rec.attempts > 1 && <Row label="retries" value={String(rec.attempts - 1)} />}
              </FlowStep>

              <Arrow />

              {/* Step 3: Stream */}
              <FlowStep label="STREAM" color="#6b8e6b">
                <Row label="ttft" value={fmt(rec.ttft_ms, "ms")} />
                <Row label="total" value={fmt(rec.total_ms, "ms")} />
                <Row label="tokens" value={fmt(rec.output_tokens, "")} />
              </FlowStep>
            </div>

            {/* Signal bars */}
            {rec.signals && Object.keys(rec.signals).length > 0 && (
              <div style={{ marginTop: 12, display: "flex", gap: 8, flexWrap: "wrap" }}>
                {(Object.entries(rec.signals) as [string, number][]).map(([k, v]) => (
                  <div key={k} style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 2 }}>
                    <div style={{ width: 28, height: 28, borderRadius: 6, background: "var(--bg-primary)", display: "flex", alignItems: "flex-end", overflow: "hidden" }}>
                      <div style={{ width: "100%", height: `${Math.round(v * 100)}%`, background: palette.accent, opacity: 0.7 }} />
                    </div>
                    <span style={{ fontSize: 9, color: "var(--text-muted)", textTransform: "uppercase", letterSpacing: "0.04em" }}>
                      {k.replace(/_/g, " ")}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function FlowStep({ label, color, children }: { label: string; color: string; children: React.ReactNode }) {
  return (
    <div style={{ flex: 1, minWidth: 120, background: "var(--bg-primary)", borderRadius: 10, padding: "10px 12px", border: `1px solid ${color}30` }}>
      <div style={{ fontSize: 9, fontWeight: 700, color, letterSpacing: "0.08em", marginBottom: 8 }}>{label}</div>
      {children}
    </div>
  );
}

function Arrow() {
  return (
    <div style={{ display: "flex", alignItems: "center", padding: "0 4px", color: "var(--text-muted)", fontSize: 16, flexShrink: 0 }}>
      →
    </div>
  );
}

function TierBadge({ tier }: { tier: string }) {
  return (
    <span style={{ background: (TIER_COLOR[tier] ?? "#555") + "22", color: TIER_COLOR[tier] ?? "var(--text-secondary)", borderRadius: 4, padding: "1px 6px", fontSize: 11, fontWeight: 600 }}>
      {tier || "—"}
    </span>
  );
}

function Row({ label, value, small }: { label: string; value: React.ReactNode; small?: boolean }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 3, gap: 8 }}>
      <span style={{ fontSize: 10, color: "var(--text-muted)", flexShrink: 0 }}>{label}</span>
      <span style={{ fontSize: small ? 10 : 12, color: "var(--text-primary)", fontWeight: 500, textAlign: "right", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 100 }}>
        {value}
      </span>
    </div>
  );
}
