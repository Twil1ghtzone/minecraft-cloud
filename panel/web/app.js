// AetherNet panel — single-page client.
//
// Architecture: the panel keeps a tiny in-memory store (`state`) of the
// cluster view, refreshed every 2s from /api/v1. Each view is a plain
// render function that reads from `state`. Monaco is loaded lazily for
// the SQL workbench.

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const state = {
  nodes: [],
  servers: [],
  groups: [],
  databases: [],
  leader: { leader_id: "", leader_address: "" },
};

async function api(path, opts = {}) {
  const r = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (r.status === 401) {
    showLogin();
    throw new Error("unauthenticated");
  }
  if (!r.ok) {
    const t = await r.text();
    throw new Error(`${r.status}: ${t}`);
  }
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

// ─── routing ────────────────────────────────────────────────────────────────
$$("#sidebar a").forEach((a) => {
  a.addEventListener("click", () => navigate(a.dataset.route));
});
function navigate(route) {
  $$("#sidebar a").forEach((a) => a.classList.toggle("active", a.dataset.route === route));
  $$(".view").forEach((v) => v.classList.add("hidden"));
  $("#view-" + route).classList.remove("hidden");
  if (route === "workbench") ensureMonaco();
}

// ─── periodic refresh ───────────────────────────────────────────────────────
async function refresh() {
  try {
    const [nodes, servers, groups, dbs, leader] = await Promise.all([
      api("/api/v1/cluster/nodes"),
      api("/api/v1/cluster/servers"),
      api("/api/v1/groups"),
      api("/api/v1/databases"),
      api("/api/v1/cluster/leader"),
    ]);
    state.nodes = nodes.nodes || [];
    state.servers = servers.servers || [];
    state.groups = groups.groups || [];
    state.databases = dbs.databases || [];
    state.leader = leader || {};
    render();
  } catch (e) {
    console.warn("refresh failed", e);
  }
}

function quorum(total) {
  return Math.floor(total / 2) + 1;
}

function render() {
  // Sidebar footer
  $("#leader-tag").textContent = "leader: " + (state.leader.leader_id || "—");
  const alive = state.nodes.filter((n) => n.state === 1).length;
  $("#quorum-tag").textContent = `quorum: ${alive}/${quorum(state.nodes.length)}`;

  renderOverview();
  renderNodes();
  renderServers();
  renderGroups();
  renderDatabases();
}

function badge(text, cls) {
  return `<span class="badge ${cls}">${text}</span>`;
}

function renderOverview() {
  const v = $("#view-overview");
  v.innerHTML = `
    <h2>Cluster overview</h2>
    <div class="grid">
      <div class="card"><h3>Nodes</h3><p>${state.nodes.length}</p></div>
      <div class="card"><h3>Servers</h3><p>${state.servers.length}</p></div>
      <div class="card"><h3>Groups</h3><p>${state.groups.length}</p></div>
      <div class="card"><h3>Databases</h3><p>${state.databases.length}</p></div>
    </div>
  `;
}

function nodeStateName(n) {
  return ["unknown", "up", "suspect", "down", "draining"][n] || "?";
}
function serverStateName(n) {
  return ["stopped","starting","ready","stopping","crashed","orphaned"][n] || "?";
}

function renderNodes() {
  const v = $("#view-nodes");
  v.innerHTML = `
    <h2>Nodes</h2>
    <table>
      <thead><tr><th>ID</th><th>Address</th><th>State</th><th>Free RAM</th><th>CPU load</th><th>Last heartbeat</th></tr></thead>
      <tbody>${state.nodes.map(n => `
        <tr>
          <td>${n.id} ${n.is_leader ? badge("leader","leader") : ""}</td>
          <td>${n.address}</td>
          <td>${badge(nodeStateName(n.state), nodeStateName(n.state))}</td>
          <td>${n.resources ? `${n.resources.memory_total_mb - n.resources.memory_used_mb} MiB` : "—"}</td>
          <td>${n.resources ? n.resources.cpu_load.toFixed(2) : "—"}</td>
          <td>${new Date(n.last_heartbeat).toLocaleTimeString()}</td>
        </tr>
      `).join("")}</tbody>
    </table>
  `;
}

function renderServers() {
  const v = $("#view-servers");
  v.innerHTML = `
    <h2>Servers <button id="srv-new">New</button></h2>
    <table>
      <thead><tr><th>Name</th><th>Group</th><th>Node</th><th>State</th><th>Players</th><th></th></tr></thead>
      <tbody>${state.servers.map(s => `
        <tr>
          <td>${s.spec.name || s.spec.id}</td>
          <td>${s.spec.group_id || "—"}</td>
          <td>${s.node_id || "—"}</td>
          <td>${badge(serverStateName(s.state), serverStateName(s.state))}</td>
          <td>${s.player_count || 0}/${s.max_players || "?"}</td>
          <td>
            <a onclick="stopServer('${s.spec.id}')">stop</a> •
            <a onclick="restartServer('${s.spec.id}')">restart</a>
          </td>
        </tr>
      `).join("")}</tbody>
    </table>
  `;
}

window.stopServer = (id) => api(`/api/v1/cluster/servers/${id}/stop`, { method: "POST" }).then(refresh);
window.restartServer = (id) => api(`/api/v1/cluster/servers/${id}/restart`, { method: "POST" }).then(refresh);

function renderGroups() {
  const v = $("#view-groups");
  v.innerHTML = `
    <h2>Groups</h2>
    <table>
      <thead><tr><th>ID</th><th>Name</th><th>Template</th><th>Min</th><th>Max</th><th>HA</th></tr></thead>
      <tbody>${state.groups.map(g => `
        <tr>
          <td>${g.id}</td><td>${g.name}</td><td>${g.template_id}</td>
          <td>${g.min_instances}</td><td>${g.max_instances || "∞"}</td>
          <td>${g.ha_required ? "yes" : "no"}</td>
        </tr>
      `).join("")}</tbody>
    </table>
  `;
}

function renderDatabases() {
  const v = $("#view-databases");
  v.innerHTML = `
    <h2>Databases <button id="db-new">New</button></h2>
    <table>
      <thead><tr><th>Name</th><th>Engine</th><th>User</th><th>Host</th><th>Size</th></tr></thead>
      <tbody>${state.databases.map(d => `
        <tr>
          <td>${d.name}</td><td>${d.engine}</td><td>${d.username}</td>
          <td>${d.host}:${d.port}</td>
          <td>${formatBytes(d.size_bytes || 0)}</td>
        </tr>
      `).join("")}</tbody>
    </table>
  `;
}

function formatBytes(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  if (n < 1024 ** 3) return (n / 1024 / 1024).toFixed(1) + " MiB";
  return (n / 1024 ** 3).toFixed(2) + " GiB";
}

// ─── SQL workbench (Monaco) ─────────────────────────────────────────────────
let monacoEditor = null;
function ensureMonaco() {
  if (monacoEditor) return;
  require.config({ paths: { vs: "https://cdn.jsdelivr.net/npm/monaco-editor@0.46.0/min/vs" } });
  require(["vs/editor/editor.main"], () => {
    monacoEditor = monaco.editor.create(document.getElementById("wb-editor"), {
      value: "-- SELECT * FROM your_table LIMIT 100;",
      language: "sql",
      theme: "vs-dark",
      automaticLayout: true,
      minimap: { enabled: false },
    });
    monacoEditor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, runQuery);
  });
  $("#wb-run").addEventListener("click", runQuery);
}

async function runQuery() {
  const dbId = $("#wb-db").value;
  if (!dbId) return;
  const sql = monacoEditor.getValue();
  $("#wb-duration").textContent = "running…";
  try {
    const r = await api(`/api/v1/databases/${dbId}/query`, {
      method: "POST",
      body: JSON.stringify({ sql, row_limit: 1000 }),
    });
    renderQueryResults(r);
    $("#wb-duration").textContent = (r.duration_ms || 0) + " ms";
  } catch (e) {
    $("#wb-results").innerHTML = `<pre style="color:var(--red)">${e.message}</pre>`;
    $("#wb-duration").textContent = "";
  }
}

function renderQueryResults(r) {
  if (!r.columns || r.columns.length === 0) {
    $("#wb-results").innerHTML = `<p>${r.rows_affected} rows affected.</p>`;
    return;
  }
  $("#wb-results").innerHTML = `
    <table>
      <thead><tr>${r.columns.map(c => `<th>${c.name}<br><small style="opacity:0.5">${c.type}</small></th>`).join("")}</tr></thead>
      <tbody>${r.rows.map(row => `<tr>${row.map(v => `<td>${v == null ? "<em>NULL</em>" : escapeHtml(String(v))}</td>`).join("")}</tr>`).join("")}</tbody>
    </table>
  `;
}

function escapeHtml(s) {
  return s.replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;");
}

refresh();
setInterval(refresh, 2000);
navigate("overview");
