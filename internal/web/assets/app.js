"use strict";

// Prompt-Response router dashboard — zero-dependency observability UI.
// Polls the router's existing JSON endpoints and renders state.

const STATUS_INTERVAL = 3000;
const USAGE_INTERVAL = 15000;
const TIERS = ["small", "medium", "large", "code", "reasoning"];

const $ = (id) => document.getElementById(id);
const expanded = new Set(); // request_ids whose detail row is open
let usageTick = 0;

async function fetchJSON(url) {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  // /readyz returns 503 with a valid JSON body when not ready — still usable.
  const body = await res.json().catch(() => null);
  return { ok: res.ok, status: res.status, body };
}

// ---- formatting helpers -------------------------------------------------

const pct = (n) => `${Math.round(n * 100)}%`;
const ms = (n) => (n == null ? "—" : `${n} ms`);
const esc = (s) =>
  String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

function tierTag(tier) {
  const t = esc(tier || "—");
  return `<span class="tier tier-${t}">${t}</span>`;
}

function utilClass(v) {
  if (v >= 0.9) return "red";
  if (v >= 0.7) return "amber";
  return "green";
}

function circuitBadge(state) {
  const cls = state === "open" ? "badge-red" : state === "half_open" ? "badge-amber" : "badge-green";
  return `<span class="badge ${cls}">${esc(state || "closed")}</span>`;
}

function percentile(sorted, p) {
  if (!sorted.length) return null;
  const idx = Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length));
  return sorted[idx];
}

// ---- renderers ----------------------------------------------------------

function renderReady(r) {
  const badge = $("ready-badge");
  if (!r || r.body == null) {
    badge.className = "badge badge-red";
    badge.textContent = "unreachable";
    return;
  }
  const ready = r.body.status === "ready";
  badge.className = `badge ${ready ? "badge-green" : "badge-red"}`;
  badge.textContent = ready ? "ready" : "not ready";
}

function renderReplicas(status) {
  const grid = $("replica-grid");
  if (!status || !status.body) {
    grid.innerHTML = `<p class="empty">Router status unavailable.</p>`;
    $("healthy-count").textContent = "— / — healthy";
    return;
  }
  const s = status.body;
  $("healthy-count").textContent = `${s.healthy_count} / ${s.total_replicas} healthy`;

  const replicas = s.replicas || [];
  if (!replicas.length) {
    grid.innerHTML = `<p class="empty">No replicas configured.</p>`;
    return;
  }
  const maxQueue = (s.config && s.config.max_queue) || 0;

  grid.innerHTML = replicas
    .map((r) => {
      const util = r.kv_cache_util || 0;
      const queuePart = maxQueue ? ` / ${maxQueue}` : "";
      return `
      <div class="replica-card ${r.healthy ? "" : "unhealthy"}">
        <div class="replica-head">
          <span class="replica-id">
            <span class="dot ${r.healthy ? "dot-green" : "dot-red"}"></span>
            ${esc(r.id)}
          </span>
          ${tierTag(r.tier)}
        </div>
        <div class="replica-model">${esc(r.model)}</div>

        <div class="metric-row"><span class="label">KV cache</span><span class="value">${pct(util)}</span></div>
        <div class="bar"><div class="bar-fill ${utilClass(util)}" style="width:${Math.min(100, util * 100)}%"></div></div>

        <div class="metric-row"><span class="label">Queue depth</span><span class="value">${r.queue_depth}${queuePart}</span></div>
        <div class="metric-row"><span class="label">Running</span><span class="value">${r.running}</span></div>

        <div class="replica-foot">
          ${circuitBadge(r.circuit)}
          <span class="value">err ${pct(r.error_rate || 0)}</span>
        </div>
      </div>`;
    })
    .join("");
}

function renderMetrics(records) {
  const box = $("metrics-summary");
  if (!records || !records.length) {
    box.innerHTML = `<p class="empty">No routing decisions recorded yet.</p>`;
    return;
  }

  const ttfts = records.map((r) => r.ttft_ms).filter((v) => v > 0).sort((a, b) => a - b);
  const tps = [];
  for (const r of records) {
    const gen = (r.total_ms - r.ttft_ms) / 1000;
    if (gen > 0 && r.output_tokens > 0) tps.push(r.output_tokens / gen);
  }
  const avgTps = tps.length ? tps.reduce((a, b) => a + b, 0) / tps.length : null;
  const cacheHits = records.filter((r) => r.cache_hit).length;
  const errors = records.filter((r) => r.status_code >= 400).length;

  const dist = TIERS.map((t) => ({ tier: t, n: records.filter((r) => r.tier === t).length }));
  const distBar = dist
    .filter((d) => d.n)
    .map((d) => `<span class="tier-${d.tier}" style="flex:${d.n};background:currentColor" title="${d.tier}: ${d.n}"></span>`)
    .join("");

  const card = (cap, big) => `<div class="metric-card"><div class="cap">${cap}</div><div class="big">${big}</div></div>`;

  box.innerHTML =
    card("Requests", records.length) +
    card("p50 TTFT", percentile(ttfts, 50) != null ? `${percentile(ttfts, 50)}<span class="cap"> ms</span>` : "—") +
    card("p95 TTFT", percentile(ttfts, 95) != null ? `${percentile(ttfts, 95)}<span class="cap"> ms</span>` : "—") +
    card("Avg throughput", avgTps != null ? `${avgTps.toFixed(1)}<span class="cap"> tok/s</span>` : "—") +
    card("Cache hit rate", pct(cacheHits / records.length)) +
    card("Error rate", pct(errors / records.length)) +
    `<div class="metric-card"><div class="cap">Tier distribution</div>
       <div class="tier-dist">${distBar || '<span style="flex:1;background:var(--border)"></span>'}</div></div>`;
}

function signalBars(signals) {
  if (!signals) return "";
  return Object.entries(signals)
    .map(([name, val]) => {
      const v = Math.max(0, Math.min(1, val));
      return `<div class="signal">
        <div class="label"><span>${esc(name)}</span><span>${val.toFixed(2)}</span></div>
        <div class="bar"><div class="bar-fill ${utilClass(v)}" style="width:${v * 100}%"></div></div>
      </div>`;
    })
    .join("");
}

function renderAudit(payload) {
  const box = $("audit-feed");
  if (!payload || !payload.body) {
    box.innerHTML = `<p class="empty">Audit trail unavailable.</p>`;
    return;
  }
  if (payload.body.enabled === false) {
    box.innerHTML = `<p class="empty">Audit trail is disabled. Enable <code>audit.enabled</code> in config.yaml.</p>`;
    return;
  }
  const records = (payload.body.records || []).slice().reverse(); // newest first
  if (!records.length) {
    box.innerHTML = `<p class="empty">No routing decisions yet — send a request to <code>/v1/chat/completions</code>.</p>`;
    return;
  }

  const rows = records
    .map((r) => {
      const score = Math.max(0, Math.min(1, r.class_score || 0));
      const open = expanded.has(r.request_id);
      const errCls = r.status_code >= 400 ? ' class="badge badge-red"' : "";
      const main = `
      <tr class="audit-row" data-id="${esc(r.request_id)}">
        <td class="mono">${esc(new Date(r.timestamp).toLocaleTimeString())}</td>
        <td>${tierTag(r.tier)}</td>
        <td><div class="score-cell">
          <div class="bar score-bar"><div class="bar-fill ${utilClass(score)}" style="width:${score * 100}%"></div></div>
          ${score.toFixed(2)}
        </div></td>
        <td class="mono">${esc(r.replica_id || "—")}</td>
        <td>${r.cache_hit ? '<span class="badge badge-green">hit</span>' : '<span class="badge badge-muted">miss</span>'}</td>
        <td class="num">${r.attempts}</td>
        <td class="num">${ms(r.ttft_ms)}</td>
        <td class="num">${ms(r.total_ms)}</td>
        <td class="num">${r.output_tokens}</td>
        <td class="num"><span${errCls}>${r.status_code}</span></td>
      </tr>`;
      if (!open) return main;
      return (
        main +
        `<tr class="detail-row"><td colspan="10">
          <div class="signals">${signalBars(r.signals)}</div>
          <div class="reason"><strong>Reason:</strong> ${esc(r.reason || "—")}
            &nbsp;·&nbsp; <strong>Request:</strong> <span class="mono">${esc(r.request_id)}</span>
            ${r.tenant ? `&nbsp;·&nbsp; <strong>Tenant:</strong> ${esc(r.tenant)}` : ""}
          </div>
        </td></tr>`
      );
    })
    .join("");

  box.innerHTML = `<table>
    <thead><tr>
      <th>Time</th><th>Tier</th><th>Class score</th><th>Replica</th><th>Cache</th>
      <th class="num">Tries</th><th class="num">TTFT</th><th class="num">Total</th>
      <th class="num">Out tok</th><th class="num">Status</th>
    </tr></thead>
    <tbody>${rows}</tbody>
  </table>`;

  box.querySelectorAll("tr.audit-row").forEach((tr) => {
    tr.addEventListener("click", () => {
      const id = tr.dataset.id;
      expanded.has(id) ? expanded.delete(id) : expanded.add(id);
      renderAudit(payload);
    });
  });
}

function renderUsage(payload) {
  const panel = $("usage-panel");
  if (!payload || !payload.body || payload.body.enabled === false) {
    panel.hidden = true;
    return;
  }
  const tenants = payload.body.tenants || {};
  const ids = Object.keys(tenants);
  panel.hidden = false;
  if (!ids.length) {
    $("usage-table").innerHTML = `<p class="empty">No tenant usage recorded yet.</p>`;
    return;
  }
  const rows = ids
    .map((id) => {
      const u = tenants[id];
      return `<tr>
        <td class="mono">${esc(id)}</td>
        <td class="num">${u.requests}</td>
        <td class="num">${u.input_tokens}</td>
        <td class="num">${u.output_tokens}</td>
        <td class="mono">${esc(new Date(u.last_seen).toLocaleString())}</td>
      </tr>`;
    })
    .join("");
  $("usage-table").innerHTML = `<table>
    <thead><tr><th>Tenant</th><th class="num">Requests</th><th class="num">Input tok</th>
      <th class="num">Output tok</th><th>Last seen</th></tr></thead>
    <tbody>${rows}</tbody></table>`;
}

function renderConfig(status) {
  const box = $("config-body");
  if (!status || !status.body || !status.body.config) {
    box.innerHTML = `<p class="empty">Configuration unavailable.</p>`;
    return;
  }
  const c = status.body.config;
  const w = c.weights || {};
  const cb = c.circuit || {};
  const retry = c.retry || {};
  const row = (label, value) => `<div class="metric-row"><span class="label">${label}</span><span class="value">${esc(value)}</span></div>`;

  box.innerHTML = `
    <div class="config-group"><h3>Routing</h3>
      ${row("threshold", c.threshold)}
      ${row("max_queue", c.max_queue)}
      ${row("affinity_ttl", c.affinity_ttl)}
    </div>
    <div class="config-group"><h3>Scoring weights</h3>
      ${row("cache_affinity", w.cache_affinity)}
      ${row("queue_depth", w.queue_depth)}
      ${row("kv_cache_pressure", w.kv_cache_pressure)}
      ${row("baseline", w.baseline)}
    </div>
    <div class="config-group"><h3>Circuit breaker</h3>
      ${row("error_threshold", cb.error_threshold)}
      ${row("window_size", cb.window_size)}
      ${row("cooldown", cb.cooldown)}
      ${row("min_samples", cb.min_samples)}
    </div>
    <div class="config-group"><h3>Retry</h3>
      ${row("max_retries", retry.max_retries)}
      ${row("timeout", retry.timeout)}
    </div>`;
}

// ---- polling loop -------------------------------------------------------

let inFlight = false;

async function refresh() {
  if (inFlight) return;
  inFlight = true;
  const indicator = $("refresh-state");
  indicator.className = "refresh-state active";

  try {
    const [ready, status, audit] = await Promise.all([
      fetchJSON("/readyz").catch(() => null),
      fetchJSON("/v1/router/status").catch(() => null),
      fetchJSON("/v1/router/audit?limit=200").catch(() => null),
    ]);

    renderReady(ready);
    renderReplicas(status);
    renderConfig(status);
    const records = audit && audit.body ? audit.body.records || [] : null;
    renderMetrics(records);
    renderAudit(audit);

    if (usageTick % Math.round(USAGE_INTERVAL / STATUS_INTERVAL) === 0) {
      const usage = await fetchJSON("/v1/router/usage").catch(() => null);
      renderUsage(usage);
    }
    usageTick++;

    $("last-updated").textContent = `Last updated ${new Date().toLocaleTimeString()}`;
  } finally {
    inFlight = false;
    setTimeout(() => {
      indicator.className = document.hidden ? "refresh-state paused" : "refresh-state";
    }, 300);
  }
}

let timer = null;
function startPolling() {
  stopPolling();
  refresh();
  timer = setInterval(refresh, STATUS_INTERVAL);
}
function stopPolling() {
  if (timer) clearInterval(timer);
  timer = null;
}

document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    stopPolling();
    $("refresh-state").className = "refresh-state paused";
  } else {
    startPolling();
  }
});
$("refresh-btn").addEventListener("click", refresh);

startPolling();
