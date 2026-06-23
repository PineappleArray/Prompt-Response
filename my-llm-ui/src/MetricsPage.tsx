import type { CSSProperties, ReactNode } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useMetrics } from "./useMetrics";
import { palette } from "./theme";

// Tier → bar color, so the same tier reads consistently across charts.
const TIER_COLORS: Record<string, string> = {
  small: "#c96442",
  code: "#d98a5f",
  medium: "#a8744f",
  large: "#7d5638",
  reasoning: "#b5573a",
};

// ── small presentational helpers ──────────────────────────────────
function Card({ title, children, style }: { title?: string; children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        background: "var(--bg-secondary)",
        border: "1px solid var(--border-color)",
        borderRadius: 16,
        padding: 20,
        ...style,
      }}
    >
      {title && (
        <div
          style={{
            fontSize: 14,
            fontWeight: 600,
            color: "var(--text-secondary)",
            marginBottom: 16,
            letterSpacing: "-0.01em",
          }}
        >
          {title}
        </div>
      )}
      {children}
    </div>
  );
}

function StatCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card>
      <div style={{ fontSize: 13, color: "var(--text-muted)", fontWeight: 500 }}>{label}</div>
      <div style={{ fontSize: 28, fontWeight: 600, marginTop: 6, letterSpacing: "-0.02em" }}>{value}</div>
      {sub && <div style={{ fontSize: 12, color: "var(--text-secondary)", marginTop: 4 }}>{sub}</div>}
    </Card>
  );
}

const fmt = new Intl.NumberFormat("en-US");
const fmtCost = (n: number) => "$" + n.toFixed(4);

function tooltipStyle() {
  return {
    background: palette.bgPrimary,
    border: `1px solid ${palette.border}`,
    borderRadius: 10,
    fontSize: 13,
    color: palette.textPrimary,
    fontFamily: "inherit",
  };
}

// ── page ──────────────────────────────────────────────────────────
export default function MetricsPage() {
  const { usage, status, tiers, loading, error, refresh } = useMetrics();

  const tenantData = usage
    ? Object.entries(usage.tenants).map(([name, u]) => ({
        name,
        input: u.input_tokens,
        output: u.output_tokens,
        requests: u.requests,
      }))
    : [];

  const tierData = tiers ? tiers.tiers : [];
  const replicaData = status ? status.replicas : [];

  const totalRequests = tenantData.reduce((s, t) => s + t.requests, 0);
  const totalInput = tenantData.reduce((s, t) => s + t.input, 0);
  const totalOutput = tenantData.reduce((s, t) => s + t.output, 0);

  return (
    <div style={{ flex: 1, overflowY: "auto", padding: "24px 16px" }}>
      <div style={{ maxWidth: 960, width: "100%", margin: "0 auto" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
          <h1 style={{ fontSize: 22, fontWeight: 600, letterSpacing: "-0.02em", margin: 0 }}>API Usage & Metrics</h1>
          <button
            onClick={refresh}
            style={{
              border: "1px solid var(--border-color)",
              background: "var(--bg-secondary)",
              borderRadius: 10,
              padding: "6px 14px",
              cursor: "pointer",
              color: "var(--text-primary)",
              fontFamily: "inherit",
              fontSize: 13,
              fontWeight: 550,
            }}
            onMouseEnter={(e) => (e.currentTarget.style.background = "var(--bg-hover)")}
            onMouseLeave={(e) => (e.currentTarget.style.background = "var(--bg-secondary)")}
          >
            Refresh
          </button>
        </div>

        {loading && <div style={{ color: "var(--text-muted)", padding: "40px 0", textAlign: "center" }}>Loading metrics…</div>}

        {error && (
          <div
            style={{
              padding: "10px 16px",
              borderRadius: 12,
              background: "#f5e6e1",
              color: "#8b3a2a",
              fontSize: 14,
              marginBottom: 16,
            }}
          >
            {error}
          </div>
        )}

        {!loading && !error && (
          <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
            {/* Summary stat cards */}
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))", gap: 16 }}>
              <StatCard label="Total Requests" value={fmt.format(totalRequests)} sub={`${tenantData.length} tenants`} />
              <StatCard label="Input Tokens" value={fmt.format(totalInput)} />
              <StatCard label="Output Tokens" value={fmt.format(totalOutput)} />
              <StatCard
                label="Cost Savings"
                value={tiers ? fmtCost(tiers.total_savings) : "—"}
                sub={tiers ? `vs ${fmtCost(tiers.total_baseline)} baseline` : undefined}
              />
              <StatCard
                label="Healthy Replicas"
                value={status ? `${status.healthy_count}/${status.total_replicas}` : "—"}
              />
            </div>

            {/* Bar graph — tokens per tenant */}
            <Card title="Token Usage by Tenant">
              <ResponsiveContainer width="100%" height={280}>
                <BarChart data={tenantData} margin={{ top: 8, right: 8, left: 8, bottom: 4 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke={palette.border} vertical={false} />
                  <XAxis dataKey="name" tick={{ fill: palette.textSecondary, fontSize: 12 }} stroke={palette.border} />
                  <YAxis tick={{ fill: palette.textMuted, fontSize: 12 }} stroke={palette.border} tickFormatter={(v) => fmt.format(v)} />
                  <Tooltip contentStyle={tooltipStyle()} formatter={(v) => fmt.format(Number(v))} cursor={{ fill: "rgba(0,0,0,0.04)" }} />
                  <Legend wrapperStyle={{ fontSize: 12, color: palette.textSecondary }} />
                  <Bar dataKey="input" name="Input" fill={palette.accent} radius={[4, 4, 0, 0]} />
                  <Bar dataKey="output" name="Output" fill="#d98a5f" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </Card>

            {/* Bar graph — requests per tier */}
            <Card title="Requests by Model Tier">
              <ResponsiveContainer width="100%" height={260}>
                <BarChart data={tierData} margin={{ top: 8, right: 8, left: 8, bottom: 4 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke={palette.border} vertical={false} />
                  <XAxis dataKey="tier" tick={{ fill: palette.textSecondary, fontSize: 12 }} stroke={palette.border} />
                  <YAxis tick={{ fill: palette.textMuted, fontSize: 12 }} stroke={palette.border} tickFormatter={(v) => fmt.format(v)} />
                  <Tooltip contentStyle={tooltipStyle()} formatter={(v) => fmt.format(Number(v))} cursor={{ fill: "rgba(0,0,0,0.04)" }} />
                  <Bar dataKey="requests" name="Requests" radius={[4, 4, 0, 0]}>
                    {tierData.map((t) => (
                      <Cell key={t.tier} fill={TIER_COLORS[t.tier] ?? palette.accent} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </Card>

            {/* Replica health table */}
            <Card title="Replica Health">
              <div style={{ overflowX: "auto" }}>
                <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
                  <thead>
                    <tr style={{ color: "var(--text-muted)", textAlign: "left" }}>
                      <th style={thStyle}>Replica</th>
                      <th style={thStyle}>Provider</th>
                      <th style={thStyle}>Tier</th>
                      <th style={thStyle}>Status</th>
                      <th style={thStyle}>Queue</th>
                      <th style={thStyle}>KV Cache</th>
                      <th style={thStyle}>Circuit</th>
                    </tr>
                  </thead>
                  <tbody>
                    {replicaData.map((r) => (
                      <tr key={r.id} style={{ borderTop: "1px solid var(--border-color)" }}>
                        <td style={tdStyle}>{r.id}</td>
                        <td style={tdStyle}>{r.provider}</td>
                        <td style={tdStyle}>{r.tier}</td>
                        <td style={{ ...tdStyle, color: r.healthy ? "#3f7d4f" : "#8b3a2a", fontWeight: 550 }}>
                          {r.healthy ? "healthy" : "down"}
                        </td>
                        <td style={tdStyle}>{r.queue_depth ?? "—"}</td>
                        <td style={tdStyle}>{r.kv_cache_util !== undefined ? (r.kv_cache_util * 100).toFixed(0) + "%" : "—"}</td>
                        <td style={tdStyle}>{r.circuit}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
          </div>
        )}
      </div>
    </div>
  );
}

const thStyle: CSSProperties = { padding: "8px 10px", fontWeight: 500 };
const tdStyle: CSSProperties = { padding: "8px 10px", color: "var(--text-primary)" };
