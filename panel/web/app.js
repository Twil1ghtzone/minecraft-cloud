// AetherNet panel — SPA client with Aetheric Flux design.
//
// Architecture: tiny in-memory `state` refreshed every 2s from /api/v1.
// Each view is a render function that writes into its <section id="view-*">.
// Monaco is loaded lazily for the SQL workbench.

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const state = {
  nodes:     [],
  servers:   [],
  groups:    [],
  databases: [],
  leader:    { leader_id: "", leader_address: "" },
};

// ─── API ─────────────────────────────────────────────────────────────────────

async function api(path, opts = {}) {
  const r = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (r.status === 401) { showLogin(); throw new Error("unauthenticated"); }
  if (!r.ok) { const t = await r.text(); throw new Error(`${r.status}: ${t}`); }
  if (r.status === 204) return null;
  return r.json();
}

function showLogin() {
  $("#login-modal").classList.remove("hidden");
}

$("#login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const token = $("#login-token").value.trim();
  if (!token) return;
  await fetch("/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  $("#login-modal").classList.add("hidden");
  refresh();
});

// ─── Routing ─────────────────────────────────────────────────────────────────

$$("#sidebar a").forEach((a) => {
  a.addEventListener("click", () => navigate(a.dataset.route));
});

function navigate(route) {
  $$("#sidebar a").forEach((a) => {
    a.classList.toggle("nav-active", a.dataset.route === route);
  });
  $$(".view").forEach((v) => v.classList.add("hidden"));
  const view = $("#view-" + route);
  if (view) view.classList.remove("hidden");
  if (route === "workbench") ensureMonaco();
}

// ─── Clock ───────────────────────────────────────────────────────────────────

function updateClock() {
  const el = $("#header-time");
  if (el) el.textContent = new Date().toLocaleTimeString();
}
updateClock();
setInterval(updateClock, 1000);

// ─── Data refresh ────────────────────────────────────────────────────────────

async function refresh() {
  try {
    const [nodes, servers, groups, dbs, leader] = await Promise.all([
      api("/api/v1/cluster/nodes"),
      api("/api/v1/cluster/servers"),
      api("/api/v1/groups"),
      api("/api/v1/databases"),
      api("/api/v1/cluster/leader"),
    ]);
    state.nodes     = nodes.nodes     || [];
    state.servers   = servers.servers || [];
    state.groups    = groups.groups   || [];
    state.databases = dbs.databases   || [];
    state.leader    = leader || {};
    render();
  } catch (e) {
    console.warn("refresh failed", e);
    const s = $("#header-status");
    if (s) s.textContent = "Connection error";
  }
}

function quorum(total) { return Math.floor(total / 2) + 1; }

function render() {
  const alive = state.nodes.filter((n) => n.state === 1).length;
  const total = state.nodes.length;

  const headerStatus = $("#header-status");
  if (headerStatus) {
    headerStatus.textContent = total
      ? `${alive} / ${total} nodes online`
      : "No nodes connected";
  }

  $("#leader-tag").textContent = "leader: " + (state.leader.leader_id || "—");
  $("#quorum-tag").textContent = `quorum: ${alive} / ${quorum(total || 1)}`;

  renderOverview();
  renderNodes();
  renderServers();
  renderGroups();
  renderTemplates();
  renderDatabases();
  renderTokens();
  updateWorkbenchDbs();
}

// ─── Design helpers ──────────────────────────────────────────────────────────

function badge(text, variant) {
  const styles = {
    up:       "background:rgba(52,211,153,0.12);color:#34d399;border:1px solid rgba(52,211,153,0.2)",
    ready:    "background:rgba(52,211,153,0.12);color:#34d399;border:1px solid rgba(52,211,153,0.2)",
    down:     "background:rgba(248,113,113,0.12);color:#f87171;border:1px solid rgba(248,113,113,0.2)",
    stopped:  "background:rgba(248,113,113,0.12);color:#f87171;border:1px solid rgba(248,113,113,0.2)",
    crashed:  "background:rgba(248,113,113,0.12);color:#f87171;border:1px solid rgba(248,113,113,0.2)",
    starting: "background:rgba(251,191,36,0.12);color:#fbbf24;border:1px solid rgba(251,191,36,0.2)",
    stopping: "background:rgba(251,191,36,0.12);color:#fbbf24;border:1px solid rgba(251,191,36,0.2)",
    suspect:  "background:rgba(251,191,36,0.12);color:#fbbf24;border:1px solid rgba(251,191,36,0.2)",
    leader:   "background:rgba(208,188,255,0.12);color:#d0bcff;border:1px solid rgba(208,188,255,0.2)",
    orphaned: "background:rgba(196,181,253,0.12);color:#c4b5fd;border:1px solid rgba(196,181,253,0.2)",
  };
  const s = styles[variant] || "background:rgba(255,255,255,0.05);color:#cbc3d7;border:1px solid rgba(255,255,255,0.1)";
  return `<span style="display:inline-flex;align-items:center;padding:1px 8px;border-radius:9999px;font-size:11px;font-family:'JetBrains Mono',monospace;font-weight:600;${s}">${text}</span>`;
}

function statusDot(stateName) {
  const map = {
    up:       { bg: "#34d399", glow: "52,211,153"  },
    ready:    { bg: "#34d399", glow: "52,211,153"  },
    down:     { bg: "#f87171", glow: "248,113,113" },
    stopped:  { bg: "#f87171", glow: "248,113,113" },
    crashed:  { bg: "#f87171", glow: "248,113,113" },
    starting: { bg: "#fbbf24", glow: "251,191,36"  },
    stopping: { bg: "#fbbf24", glow: "251,191,36"  },
    suspect:  { bg: "#fbbf24", glow: "251,191,36"  },
    orphaned: { bg: "#c4b5fd", glow: "196,181,253" },
  };
  const c = map[stateName] || { bg: "#6b7280", glow: null };
  const shadow = c.glow ? `;box-shadow:0 0 6px rgba(${c.glow},0.55)` : "";
  return `<span style="display:inline-block;width:8px;height:8px;border-radius:9999px;flex-shrink:0;background:${c.bg}${shadow}"></span>`;
}

function viewHeader(title, subtitle = "", actionHtml = "") {
  return `
    <div style="display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:1.5rem" class="animate-fade-up">
      <div>
        <h2 style="margin:0 0 4px;font-size:1.4rem;font-weight:600;color:#e1e1ef;letter-spacing:-0.01em">${title}</h2>
        ${subtitle ? `<p style="margin:0;font-size:0.8125rem;color:#cbc3d7">${subtitle}</p>` : ""}
      </div>
      ${actionHtml}
    </div>
  `;
}

function glassCard(content, delay = 0) {
  return `<div class="animate-fade-up" style="background:rgba(29,31,41,0.4);backdrop-filter:blur(32px);border:1px solid rgba(255,255,255,0.05);border-radius:1rem;overflow:hidden;box-shadow:0 8px 32px rgba(0,0,0,0.25);animation-delay:${delay}s">${content}</div>`;
}

function glassTable(cols, bodyRows, delay = 0) {
  const head = cols.map(c =>
    `<th style="padding:12px 16px;text-align:left;font-size:11px;font-family:'JetBrains Mono',monospace;font-weight:600;color:rgba(203,195,215,0.55);text-transform:uppercase;letter-spacing:0.06em;border-bottom:1px solid rgba(255,255,255,0.05)">${c}</th>`
  ).join("");

  const empty = `<tr><td colspan="${cols.length}" style="padding:40px 16px;text-align:center;font-size:0.8125rem;color:rgba(203,195,215,0.3);font-family:'JetBrains Mono',monospace">No data</td></tr>`;

  const tableContent = `
    <div style="overflow-x:auto">
      <table style="width:100%;border-collapse:collapse;font-size:0.875rem">
        <thead><tr>${head}</tr></thead>
        <tbody>${bodyRows || empty}</tbody>
      </table>
    </div>
  `;
  return glassCard(tableContent, delay);
}

function btnPrimary(label, icon, onclick) {
  return `<button onclick="${onclick}" style="display:inline-flex;align-items:center;gap:6px;padding:8px 16px;border-radius:0.75rem;font-size:0.8125rem;font-weight:600;color:#d0bcff;background:rgba(208,188,255,0.09);border:1px solid rgba(208,188,255,0.2);cursor:pointer;transition:all 0.15s" onmouseover="this.style.background='rgba(208,188,255,0.16)'" onmouseout="this.style.background='rgba(208,188,255,0.09)'"><span class="material-symbols-outlined" style="font-size:15px">${icon}</span>${label}</button>`;
}

function btnAction(label, icon, onclick, type = "ghost") {
  const styles = {
    ghost:  "color:rgba(203,195,215,0.7);background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.08)",
    danger: "color:#f87171;background:rgba(248,113,113,0.07);border:1px solid rgba(248,113,113,0.15)",
  };
  const s = styles[type] || styles.ghost;
  return `<button onclick="${onclick}" style="display:inline-flex;align-items:center;gap:4px;padding:4px 10px;border-radius:0.5rem;font-size:11px;font-weight:600;cursor:pointer;transition:background 0.12s;${s}"><span class="material-symbols-outlined" style="font-size:13px">${icon}</span>${label}</button>`;
}

function tdCell(content) {
  return `<td style="padding:11px 16px;border-bottom:1px solid rgba(255,255,255,0.04)">${content}</td>`;
}
function tdText(text, mono = false) {
  return tdCell(`<span style="font-size:0.8125rem;color:#e1e1ef${mono ? ";font-family:'JetBrains Mono',monospace;font-size:12px" : ""}">${text}</span>`);
}
function tdMuted(text) {
  return tdCell(`<span style="font-size:12px;color:rgba(203,195,215,0.5);font-family:'JetBrains Mono',monospace">${text}</span>`);
}

// ─── Views ───────────────────────────────────────────────────────────────────

function nodeStateName(n) {
  return ["unknown","up","suspect","down","draining"][n] || "?";
}
function serverStateName(n) {
  return ["stopped","starting","ready","stopping","crashed","orphaned"][n] || "?";
}

function renderOverview() {
  const alive   = state.nodes.filter((n) => n.state === 1).length;
  const running = state.servers.filter((s) => s.state === 2).length;
  const players = state.servers.reduce((acc, s) => acc + (s.player_count || 0), 0);

  function statCard(icon, value, label, subtext, color, delay) {
    return `
      <div class="animate-fade-up" style="background:rgba(29,31,41,0.4);backdrop-filter:blur(32px);border:1px solid rgba(255,255,255,0.05);border-radius:1rem;padding:20px;box-shadow:0 4px 20px rgba(0,0,0,0.2);transition:border-color 0.2s;animation-delay:${delay}s">
        <div style="display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:16px">
          <span class="material-symbols-outlined" style="font-size:22px;color:${color}">${icon}</span>
          <span style="font-size:11px;font-family:'JetBrains Mono',monospace;color:rgba(203,195,215,0.35)">${subtext}</span>
        </div>
        <p style="margin:0 0 4px;font-size:2rem;font-weight:700;color:#e1e1ef;letter-spacing:-0.02em;line-height:1">${value}</p>
        <p style="margin:0;font-size:0.8125rem;color:#cbc3d7">${label}</p>
      </div>
    `;
  }

  const recentRows = state.servers.slice(0, 6).map(s => {
    const sn = serverStateName(s.state);
    return `<tr>
      ${tdCell(`<div style="display:flex;align-items:center;gap:8px">${statusDot(sn)}<span style="font-size:0.875rem;font-weight:500;color:#e1e1ef">${escapeHtml(s.spec.name || s.spec.id)}</span></div>`)}
      ${tdMuted(s.spec.group_id || "—")}
      ${tdCell(badge(sn, sn))}
      ${tdMuted(`${s.player_count || 0} / ${s.max_players || "?"}`)}
    </tr>`;
  }).join("");

  $("#view-overview").innerHTML = `
    ${viewHeader("Overview", "Cluster status and active sessions")}
    <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:16px;margin-bottom:28px">
      ${statCard("device_hub", state.nodes.length,     "Nodes",     `${alive} online`,   "#d0bcff", 0)}
      ${statCard("dns",        state.servers.length,   "Servers",   `${running} running`, "#4cd7f6", 0.05)}
      ${statCard("folder_open",state.groups.length,    "Groups",    "configured",         "#ffafd3", 0.1)}
      ${statCard("person",     players,                "Players",   "online now",         "#a078ff", 0.15)}
    </div>
    ${glassTable(["Server","Group","State","Players"], recentRows, 0.2)}
  `;
}

function renderNodes() {
  const rows = state.nodes.map(n => {
    const sn = nodeStateName(n.state);
    const freeRam = n.resources
      ? formatBytes((n.resources.memory_total_mb - n.resources.memory_used_mb) * 1024 * 1024)
      : "—";
    const cpu = n.resources ? n.resources.cpu_load.toFixed(2) + "%" : "—";
    return `<tr>
      ${tdCell(`<div style="display:flex;align-items:center;gap:8px">${statusDot(sn)}<span style="font-size:12px;font-family:'JetBrains Mono',monospace;color:#e1e1ef">${escapeHtml(n.id)}</span>${n.is_leader ? " " + badge("leader","leader") : ""}</div>`)}
      ${tdMuted(n.address)}
      ${tdCell(badge(sn, sn))}
      ${tdMuted(freeRam)}
      ${tdMuted(cpu)}
      ${tdMuted(new Date(n.last_heartbeat).toLocaleTimeString())}
    </tr>`;
  }).join("");

  $("#view-nodes").innerHTML = `
    ${viewHeader("Nodes", "Cluster member nodes and their health")}
    ${glassTable(["Node ID","Address","State","Free RAM","CPU Load","Heartbeat"], rows)}
  `;
}

function renderServers() {
  const rows = state.servers.map(s => {
    const sn = serverStateName(s.state);
    return `<tr>
      ${tdCell(`<div style="display:flex;align-items:center;gap:8px">${statusDot(sn)}<span style="font-size:0.875rem;font-weight:500;color:#e1e1ef">${escapeHtml(s.spec.name || s.spec.id)}</span></div>`)}
      ${tdMuted(s.spec.group_id || "—")}
      ${tdMuted(s.node_id || "—")}
      ${tdCell(badge(sn, sn))}
      ${tdMuted(`${s.player_count || 0} / ${s.max_players || "?"}`)}
      ${tdCell(`<div style="display:flex;gap:6px">${btnAction("Stop","stop",`stopServer('${s.spec.id}')`, "danger")}${btnAction("Restart","restart_alt",`restartServer('${s.spec.id}')`)}</div>`)}
    </tr>`;
  }).join("");

  $("#view-servers").innerHTML = `
    ${viewHeader("Servers", "Managed game server instances", btnPrimary("New Server","add","openNewServerModal()"))}
    ${glassTable(["Name","Group","Node","State","Players","Actions"], rows)}
  `;
}

window.stopServer    = (id) => api(`/api/v1/cluster/servers/${id}/stop`,    { method: "POST" }).then(refresh);
window.restartServer = (id) => api(`/api/v1/cluster/servers/${id}/restart`, { method: "POST" }).then(refresh);
window.openNewServerModal = () => console.log("TODO: new server modal");

function renderGroups() {
  const rows = state.groups.map(g => `<tr>
    ${tdMuted(g.id)}
    ${tdText(escapeHtml(g.name))}
    ${tdMuted(g.template_id)}
    ${tdMuted(String(g.min_instances))}
    ${tdMuted(g.max_instances ? String(g.max_instances) : "∞")}
    ${tdCell(g.ha_required ? badge("HA","leader") : badge("off","unknown"))}
  </tr>`).join("");

  $("#view-groups").innerHTML = `
    ${viewHeader("Groups", "Server group definitions and scaling policies")}
    ${glassTable(["ID","Name","Template","Min","Max","HA"], rows)}
  `;
}

function renderTemplates() {
  $("#view-templates").innerHTML = `
    ${viewHeader("Templates", "Server templates and OCI image configurations")}
    ${glassCard(`
      <div style="padding:64px 32px;text-align:center">
        <span class="material-symbols-outlined" style="font-size:40px;color:rgba(203,195,215,0.2);display:block;margin-bottom:12px">layers</span>
        <p style="margin:0 0 6px;font-size:0.875rem;color:rgba(203,195,215,0.5)">Templates are managed via the daemon API.</p>
        <p style="margin:0;font-size:11px;font-family:'JetBrains Mono',monospace;color:rgba(203,195,215,0.25)">POST /api/v1/templates</p>
      </div>
    `)}
  `;
}

function renderDatabases() {
  const rows = state.databases.map(d => `<tr>
    ${tdText(escapeHtml(d.name))}
    ${tdCell(badge(d.engine, "leader"))}
    ${tdMuted(d.username)}
    ${tdMuted(`${d.host}:${d.port}`)}
    ${tdMuted(formatBytes(d.size_bytes || 0))}
  </tr>`).join("");

  $("#view-databases").innerHTML = `
    ${viewHeader("Databases", "Managed MariaDB/MySQL database instances", btnPrimary("New Database","add","openNewDbModal()"))}
    ${glassTable(["Name","Engine","User","Host","Size"], rows)}
  `;
}

window.openNewDbModal = () => console.log("TODO: new database modal");

function updateWorkbenchDbs() {
  const sel = $("#wb-db");
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = `<option value="">Select database…</option>` +
    state.databases.map(d =>
      `<option value="${escapeHtml(String(d.id))}"${d.id === prev ? " selected" : ""}>${escapeHtml(d.name)}</option>`
    ).join("");
}

function renderTokens() {
  $("#view-tokens").innerHTML = `
    ${viewHeader("API Tokens", "Bearer tokens for programmatic access")}
    ${glassCard(`
      <div style="padding:64px 32px;text-align:center">
        <span class="material-symbols-outlined" style="font-size:40px;color:rgba(203,195,215,0.2);display:block;margin-bottom:12px">key</span>
        <p style="margin:0 0 6px;font-size:0.875rem;color:rgba(203,195,215,0.5)">Token management coming soon.</p>
        <p style="margin:0;font-size:11px;font-family:'JetBrains Mono',monospace;color:rgba(203,195,215,0.25)">POST /api/v1/tokens</p>
      </div>
    `)}
  `;
}

// ─── SQL Workbench (Monaco) ──────────────────────────────────────────────────

let monacoEditor = null;

function ensureMonaco() {
  if (monacoEditor) return;
  require.config({ paths: { vs: "https://cdn.jsdelivr.net/npm/monaco-editor@0.46.0/min/vs" } });
  require(["vs/editor/editor.main"], () => {
    monaco.editor.defineTheme("aether-dark", {
      base: "vs-dark",
      inherit: true,
      rules: [
        { token: "keyword.sql",  foreground: "d0bcff", fontStyle: "bold" },
        { token: "string.sql",   foreground: "4cd7f6" },
        { token: "comment.sql",  foreground: "494454", fontStyle: "italic" },
      ],
      colors: {
        "editor.background":              "#1d1f29",
        "editor.lineHighlightBackground": "#282933",
        "editorLineNumber.foreground":    "#494454",
        "editorLineNumber.activeForeground": "#cbc3d7",
        "editor.selectionBackground":     "#a078ff33",
        "editorCursor.foreground":        "#d0bcff",
      },
    });
    monacoEditor = monaco.editor.create(document.getElementById("wb-editor"), {
      value: "-- SELECT * FROM your_table LIMIT 100;",
      language: "sql",
      theme: "aether-dark",
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      fontFamily: "'JetBrains Mono', monospace",
      lineNumbers: "on",
      scrollBeyondLastLine: false,
      padding: { top: 16, bottom: 16 },
      renderLineHighlight: "gutter",
    });
    monacoEditor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, runQuery);
    $("#wb-run").addEventListener("click", runQuery);
  });
}

async function runQuery() {
  const dbId = $("#wb-db").value;
  if (!dbId) return;
  const sql = monacoEditor.getValue();
  const dur = $("#wb-duration");
  if (dur) dur.textContent = "running…";
  try {
    const r = await api(`/api/v1/databases/${dbId}/query`, {
      method: "POST",
      body: JSON.stringify({ sql, row_limit: 1000 }),
    });
    renderQueryResults(r);
    if (dur) dur.textContent = (r.duration_ms || 0) + " ms";
  } catch (e) {
    $("#wb-results").innerHTML = `
      <div style="background:rgba(248,113,113,0.08);border:1px solid rgba(248,113,113,0.2);border-radius:0.75rem;padding:16px;font-family:'JetBrains Mono',monospace;font-size:12px;color:#f87171">${escapeHtml(e.message)}</div>
    `;
    if (dur) dur.textContent = "";
  }
}

function renderQueryResults(r) {
  const res = $("#wb-results");
  if (!r.columns || r.columns.length === 0) {
    res.innerHTML = `
      <div style="background:rgba(76,215,246,0.06);border:1px solid rgba(76,215,246,0.15);border-radius:0.75rem;padding:14px 16px;font-family:'JetBrains Mono',monospace;font-size:13px;color:#4cd7f6">
        ${r.rows_affected} row${r.rows_affected !== 1 ? "s" : ""} affected.
      </div>
    `;
    return;
  }
  const headCells = r.columns.map(c =>
    `<th style="padding:10px 14px;text-align:left;font-family:'JetBrains Mono',monospace;font-size:11px;font-weight:600;color:rgba(203,195,215,0.5);text-transform:uppercase;letter-spacing:0.06em;border-bottom:1px solid rgba(255,255,255,0.05);white-space:nowrap;position:sticky;top:0;background:#282933">
      ${escapeHtml(c.name)}<br>
      <span style="font-size:10px;opacity:0.5;text-transform:none;letter-spacing:0">${escapeHtml(c.type)}</span>
    </th>`
  ).join("");
  const bodyRows = r.rows.map(row =>
    `<tr>${row.map(v =>
      `<td style="padding:8px 14px;font-family:'JetBrains Mono',monospace;font-size:12px;color:#e1e1ef;border-bottom:1px solid rgba(255,255,255,0.03);white-space:nowrap">${
        v == null
          ? `<em style="color:rgba(203,195,215,0.25)">NULL</em>`
          : escapeHtml(String(v))
      }</td>`
    ).join("")}</tr>`
  ).join("");

  res.innerHTML = `
    <div style="background:rgba(29,31,41,0.4);backdrop-filter:blur(32px);border:1px solid rgba(255,255,255,0.05);border-radius:1rem;overflow:hidden">
      <div style="overflow:auto;max-height:50vh">
        <table style="width:100%;border-collapse:collapse">
          <thead><tr>${headCells}</tr></thead>
          <tbody>${bodyRows}</tbody>
        </table>
      </div>
    </div>
  `;
}

// ─── Utilities ───────────────────────────────────────────────────────────────

function formatBytes(n) {
  if (n < 1024)           return n + " B";
  if (n < 1024 * 1024)    return (n / 1024).toFixed(1) + " KiB";
  if (n < 1024 ** 3)      return (n / 1024 / 1024).toFixed(1) + " MiB";
  return (n / 1024 ** 3).toFixed(2) + " GiB";
}

function escapeHtml(s) {
  return s.replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;");
}

// ─── Boot ────────────────────────────────────────────────────────────────────

refresh();
setInterval(refresh, 2000);
navigate("overview");
