/*
 * SessionGuard Master console. Plain JavaScript (no build step) on top of
 * /ui/sg.js and /ui/policy-editor.js. Hash routes:
 *   #/dashboard  #/servers  #/servers/<id>[/<tab>]  #/sessions  #/farms
 *   #/resources  #/access   #/policies  #/alerts  #/history  #/audit
 *
 * All server data is escaped with esc() before it is inserted as HTML.
 */
(function () {
  "use strict";
  const { $, qs, qsa, esc, api, icon } = SG;

  const REFRESH_MS = 5000;
  const SLOW_MS = 30000;
  const SPARK_POINTS = 60;

  const S = {
    me: null,
    perms: new Set(),
    dash: null,
    agents: [],
    farms: [],
    resources: [],
    leases: [],
    alerts: [],
    history: [],
    audit: [],
    policyHistory: null,
    globalPolicy: null,
    access: null,
    identities: null,
    adminSessions: null,
    metrics: {},
    paused: false,
    error: null,
    lastOK: null,
    loading: false,
    route: { name: "dashboard", params: [] },
    lastHash: "",
    loadedAt: {},
    detail: null,
    selection: new Set(),
    filters: { servers: "all", sessions: "all", alerts: "active", resources: "all", events: "all", audit: "all" },
    hist: { user: "", agent: "", limit: 1000 },
  };
  const can = (p) => S.perms.has(p);
  const noPerm = (p) => (can(p) ? "" : ' disabled title="Fehlende Berechtigung: ' + esc(p) + '"');

  // ------------------------------------------------------------ helpers --
  const modeBadge = (m) => {
    m = m || "online";
    return SG.badge(m === "online" ? "Online" : m === "drain" ? "Drain" : "Maintenance", m === "online" ? "ok" : m === "drain" ? "warn" : "bad");
  };
  const onlineBadge = (on) => (on ? SG.badge("Online", "ok", "dot") : SG.badge("Offline", "", "dot"));
  const agentPolicy = (a) => (a.desired_policy && a.desired_policy.revision ? a.desired_policy : (a.snapshot || {}).policy);
  const controlEnabled = (a) => {
    const p = agentPolicy(a);
    return !!(p && p.sessions && p.sessions.control_enabled);
  };
  const memPct = (v) => (v && v.memory_total ? ((v.memory_total - v.memory_available) * 100) / v.memory_total : 0);
  const diskPct = (v) => (v && v.disk_total ? ((v.disk_total - v.disk_free) * 100) / v.disk_total : 0);
  const agentById = (id) => S.agents.find((a) => a.id === id);
  const agentName = (id) => (agentById(id) || {}).name || id;
  const farmById = (id) => S.farms.find((f) => f.id === id);
  const userOf = (s) => (s.domain ? s.domain + "\\" : "") + (s.user || "");
  const stateBadge = (st) => {
    const s = String(st || "").toLowerCase();
    return SG.badge(st || "–", s === "active" ? "ok" : s === "disconnected" ? "warn" : "");
  };
  const link = (route, html) => '<a href="#/' + route + '">' + html + "</a>";
  const policyTarget = (t) => {
    if (t === "global") return "Global";
    if (t.startsWith("agent:")) return "Server " + agentName(t.slice(6));
    if (t.startsWith("farm:")) return "Farm " + ((farmById(t.slice(5)) || {}).name || t.slice(5));
    return t;
  };

  function agentMatchesFarm(a, f) {
    if (!a || !f) return false;
    if ((f.agent_ids || []).includes(a.id) || (a.farm_ids || []).includes(f.id)) return true;
    const req = Object.entries(f.required_tags || {});
    return req.length > 0 && req.every(([k, v]) => String((a.tags || {})[k] ?? "") === String(v));
  }
  const farmMembers = (f) => S.agents.filter((a) => agentMatchesFarm(a, f));

  function resourceReadiness(x) {
    if (x.kind !== "remoteapp") return { text: "Desktop", cls: "", detail: "" };
    if (!x.manage_remote_app) return { text: "Extern verwaltet", cls: "", detail: "RemoteApp wird nicht durch SessionGuard provisioniert" };
    const f = farmById(x.farm_id),
      members = f ? farmMembers(f) : [];
    let ready = 0;
    const detail = [];
    members.forEach((a) => {
      const st = ((a.snapshot || {}).remote_apps || []).find(
        (r) => r.resource_id === x.id || (!r.resource_id && String(r.alias || "").toLowerCase() === String(x.remote_app || "").replace(/^\|\|/, "").toLowerCase()),
      );
      const ok = !!(st && st.published && st.path_exists && st.in_sync && !st.error);
      if (ok) ready++;
      detail.push((a.name || a.id) + ": " + (ok ? "bereit" : st && st.error ? st.error : "noch kein Status"));
    });
    if (!members.length) return { text: "Keine Farm-Server", cls: "warn", detail: "Der Farm sind keine Server zugeordnet" };
    return { text: ready + "/" + members.length + " bereit", cls: ready === members.length ? "ok" : "bad", detail: detail.join("\n") };
  }

  function recordMetrics() {
    S.agents.forEach((a) => {
      const v = (a.snapshot || {}).server || {};
      const m = (S.metrics[a.id] = S.metrics[a.id] || { cpu: [], ram: [], at: "" });
      const stamp = (a.snapshot || {}).collected_at || a.last_seen;
      if (stamp && stamp === m.at) return;
      m.at = stamp;
      m.cpu.push(Number(v.cpu_percent || 0));
      m.ram.push(memPct(v));
      if (m.cpu.length > SPARK_POINTS) m.cpu.shift();
      if (m.ram.length > SPARK_POINTS) m.ram.shift();
    });
  }

  function allSessions() {
    const out = [];
    S.agents.forEach((a) =>
      ((a.snapshot || {}).sessions || []).forEach((s) => {
        if (s.user) out.push(Object.assign({ agent: a, key: a.id + ":" + s.id }, s));
      }),
    );
    return out;
  }

  // ------------------------------------------------------------ actions --
  async function sessionAction(agentId, sid, action, label) {
    const body = { action };
    if (action === "message") {
      const m = await SG.prompt({ title: "Nachricht senden", subtitle: label, label: "Nachricht", multiline: true, confirm: "Senden", icon: "message" });
      if (m === null) return false;
      body.message = m;
      body.title = "SessionGuard";
    }
    if (action === "logoff" && !(await SG.confirm({ title: "Sitzung abmelden", message: (label || "Sitzung " + sid) + " wirklich abmelden? Die Profilsicherung startet nach dem Sitzungsende.", confirm: "Abmelden", danger: true }))) return false;
    if (action === "disconnect" && !(await SG.confirm({ title: "Sitzung trennen", message: (label || "Sitzung " + sid) + " trennen? Die Sitzung bleibt auf dem Server erhalten.", confirm: "Trennen" }))) return false;
    try {
      await api("/api/v1/agents/" + encodeURIComponent(agentId) + "/sessions/" + sid + "/action", { method: "POST", body });
      SG.toast("Sitzungsaktion an den Agent gesendet");
      setTimeout(() => tick(true), 1200);
      return true;
    } catch (e) {
      SG.fail(e);
      return false;
    }
  }

  async function bulkAction(agentId, action, scope) {
    const body = { action, scope };
    if (action === "message") {
      const m = await SG.prompt({ title: "Nachricht an alle Benutzer", subtitle: agentName(agentId), label: "Nachricht", multiline: true, confirm: "An alle senden", icon: "message" });
      if (m === null) return;
      body.message = m;
      body.title = "SessionGuard";
    }
    if (action === "logoff" && !(await SG.confirm({ title: "Getrennte Sitzungen abmelden", message: "Alle getrennten Sitzungen auf " + agentName(agentId) + " abmelden? Die Profil-Pipeline läuft anschließend.", confirm: "Abmelden", danger: true }))) return;
    try {
      const r = await api("/api/v1/agents/" + encodeURIComponent(agentId) + "/sessions/bulk", { method: "POST", body });
      SG.toast((r.queued || 0) + " Sitzungsaktionen eingeplant");
    } catch (e) {
      SG.fail(e);
    }
  }

  async function saveControl(agentId, body, msg) {
    await api("/api/v1/agents/" + encodeURIComponent(agentId) + "/control", { method: "PATCH", body });
    SG.toast(msg || "Serversteuerung gespeichert");
    await tick(true);
  }

  // ------------------------------------------------------------- chrome --
  function renderChrome() {
    const live = $("live"),
      txt = $("liveText");
    live.classList.toggle("paused", S.paused);
    live.classList.toggle("error", !!S.error && !S.paused);
    txt.textContent = S.paused ? "Pausiert" : S.error ? "Verbindung gestört" : S.lastOK ? "Live · " + S.lastOK.toLocaleTimeString("de-DE") : "Verbinde…";
    $("pauseBtn").innerHTML = icon(S.paused ? "play" : "pause");
    $("pauseBtn").title = S.paused ? "Live-Aktualisierung fortsetzen" : "Live-Aktualisierung pausieren";
    $("banner").innerHTML =
      S.error && !S.paused
        ? '<div class="banner">' + icon("alert") + "Master nicht erreichbar oder Anfrage fehlgeschlagen: " + esc(S.error.message || S.error) + " – angezeigte Daten können veraltet sein.</div>"
        : "";
    const d = S.dash || {};
    const alerts = d.active_alerts || 0;
    $("navAlerts").textContent = alerts || "";
    $("navAlerts").className = "count" + (alerts ? " bad" : "");
    $("navAlerts").hidden = !alerts;
    const sess = S.agents.reduce((n, a) => n + (a.total_sessions || 0), 0);
    $("navSessions").textContent = sess || "";
    $("navSessions").hidden = !sess;
    $("navServers").textContent = S.agents.length ? S.agents.filter((a) => a.online).length + "/" + S.agents.length : "";
    $("navServers").hidden = !S.agents.length;
    qsa(".nav a[data-route]").forEach((a) => a.classList.toggle("active", a.dataset.route === S.route.name || (a.dataset.route === "servers" && S.route.name === "server")));
  }

  function renderUser() {
    const u = (S.me && S.me.user) || {};
    const name = u.name || u.email || u.sub || "–";
    $("userName").textContent = name;
    $("userName").title = u.email || name;
    $("userRoles").textContent = ((S.me && S.me.roles) || []).join(", ") || "keine Rolle";
    $("userAvatar").textContent = (name.match(/[A-Za-zÄÖÜäöü0-9]/g) || ["?"]).slice(0, 2).join("").toUpperCase();
  }

  function setCrumbs(parts) {
    $("crumbs").innerHTML = parts
      .map((p, i) => (i < parts.length - 1 && p.href ? '<a href="' + p.href + '">' + esc(p.label) + "</a>" + icon("chevron") : "<h1>" + esc(p.label) + "</h1>"))
      .join("");
    document.title = parts[parts.length - 1].label + " · SessionGuard";
  }

  const pageHead = (title, text, actions) =>
    '<div class="page-head"><div><h2>' + esc(title) + "</h2>" + (text ? "<p>" + esc(text) + "</p>" : "") + '</div><div class="row">' + (actions || "") + "</div></div>";

  // ============================================================ VIEWS ====
  const views = {};

  // ---------------------------------------------------------- dashboard --
  views.dashboard = {
    title: "Dashboard",
    interval: REFRESH_MS,
    async load() {
      const [al, hi] = await Promise.all([api("/api/v1/alerts"), api("/api/v1/history?limit=8")]);
      S.alerts = al.alerts || [];
      S.history = hi.history || [];
    },
    mount() {
      $("view").innerHTML =
        pageHead("Infrastruktur auf einen Blick", "Live-Status der RDS-Farm, Sitzungen, Profile und Broker-Ressourcen.") +
        '<div class="stats" id="dStats"></div><div class="grid grid-3"><div class="card span-2"><div class="card-head"><div><h3>Terminalserver</h3><div class="hint">Health, CPU und RAM je Server</div></div><a class="btn sm" href="#/servers">Alle Server' +
        icon("chevron") +
        '</a></div><div class="card-body"><div class="tiles" id="dTiles"></div></div></div><div class="stack"><div class="card"><div class="card-head"><h3>Aktive Alerts</h3><a class="btn sm ghost" href="#/alerts">Alle</a></div><div class="card-body flush" id="dAlerts"></div></div><div class="card"><div class="card-head"><h3>Letzte Ereignisse</h3><a class="btn sm ghost" href="#/history">Historie</a></div><div class="card-body flush" id="dEvents"></div></div></div></div>';
    },
    update() {
      const a = S.agents,
        d = S.dash || {};
      const online = a.filter((x) => x.online).length;
      const active = a.reduce((n, x) => n + (x.active_sessions || 0), 0);
      const disc = a.reduce((n, x) => n + (x.disconnected_sessions || 0), 0);
      const jobs = a.reduce((n, x) => n + ((x.snapshot || {}).profile_jobs || []).length, 0);
      const cleanup = a.reduce((n, x) => n + ((x.snapshot || {}).pending_cleanup || []).length, 0);
      const stat = (href, ic, label, value, sub, tone) =>
        '<a class="stat' + (tone ? " tone-" + tone : "") + '" href="' + href + '"><span class="stat-label">' + icon(ic) + esc(label) + '</span><span class="stat-value">' + esc(value) + "</span>" + (sub ? '<span class="stat-sub">' + esc(sub) + "</span>" : "") + "</a>";
      $("dStats").innerHTML =
        stat("#/servers", "server", "Server online", online + "/" + a.length, a.length - online ? a.length - online + " offline" : "alle erreichbar", a.length - online ? "warn" : "ok") +
        stat("#/sessions", "users", "Aktive Sitzungen", active, disc + " getrennt") +
        stat("#/servers", "box", "Profil-Jobs", jobs, cleanup + " Cleanup ausstehend") +
        stat("#/farms", "layers", "Farms", d.farms || 0, (d.resources || 0) + " Resources") +
        stat("#/access", "shield", "Access-Sessions", d.access_sessions || 0, "Guacamole") +
        stat("#/alerts", "bell", "Aktive Alerts", d.active_alerts || 0, "", d.active_alerts ? "bad" : "ok");
      $("dTiles").innerHTML = a.length
        ? a
            .map((x) => {
              const v = (x.snapshot || {}).server || {},
                m = S.metrics[x.id] || { cpu: [] };
              return (
                '<a class="tile" href="#/servers/' +
                encodeURIComponent(x.id) +
                '"><div class="tile-head"><span class="tile-name">' +
                esc(x.name) +
                '</span><span class="row" style="gap:4px">' +
                (x.maintenance_mode && x.maintenance_mode !== "online" ? modeBadge(x.maintenance_mode) : "") +
                onlineBadge(x.online) +
                '</span></div><div class="meters">' +
                SG.healthMeter(((x.snapshot || {}).health || {}).score) +
                SG.meter("CPU", v.cpu_percent) +
                SG.meter("RAM", memPct(v)) +
                "</div>" +
                SG.spark(m.cpu) +
                '<div class="row between small muted"><span>' +
                icon("users") +
                " " +
                (x.active_sessions || 0) +
                " aktiv · " +
                (x.disconnected_sessions || 0) +
                " getrennt</span><span>" +
                SG.time(x.last_seen) +
                "</span></div></a>"
              );
            })
            .join("")
        : SG.empty("Noch keine Agents registriert.", "server");
      const act = S.alerts.filter((x) => x.active);
      $("dAlerts").innerHTML = act.length
        ? '<div class="list" style="padding:12px">' +
          act
            .slice(0, 6)
            .map((x) => '<div class="item" style="gap:4px"><div class="row between"><strong>' + esc(x.type) + "</strong>" + SG.badge(x.hostname || "–", "bad") + '</div><div class="small">' + esc(x.message) + '</div><div class="small muted">' + SG.time(x.last_seen_at) + "</div></div>")
            .join("") +
          "</div>"
        : SG.empty("Keine aktiven Alerts.", "check");
      $("dEvents").innerHTML = S.history.length
        ? SG.table({
            id: "dEvents",
            rows: S.history.slice().reverse(),
            foot: false,
            columns: [
              { key: "time", label: "Zeit", html: (r) => SG.time(r.time), sort: false },
              { key: "user", label: "Benutzer", html: (r) => esc(r.user || "–") + '<div class="small muted">' + esc(r.hostname) + "</div>", sort: false },
              { key: "event", label: "Ereignis", html: (r) => SG.badge(r.event, eventKind(r.event)), sort: false },
            ],
          })
        : SG.empty("Noch keine Session-Historie.", "clock");
    },
  };
  const eventKind = (e) => {
    e = String(e || "").toLowerCase();
    return e.includes("logoff") ? "" : e.includes("disconnect") ? "warn" : e.includes("logon") || e.includes("ready") || e.includes("reconnect") ? "ok" : "info";
  };

  // ------------------------------------------------------------ servers --
  views.servers = {
    title: "Terminalserver",
    interval: REFRESH_MS,
    mount() {
      SG.onTable("servers", () => this.update());
      $("view").innerHTML =
        pageHead("Terminalserver", "Server anklicken, um Sitzungen, Prozesse, Profile, Steuerung und Policy zu öffnen.") +
        '<div class="card"><div class="toolbar">' +
        SG.searchBox("servers", "Server, Version, Tag suchen…") +
        '<div class="btn-group" id="srvFilter">' +
        [
          ["all", "Alle"],
          ["online", "Online"],
          ["offline", "Offline"],
          ["drain", "Drain"],
          ["maintenance", "Maintenance"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-f="' + k + '">' + l + "</button>")
          .join("") +
        '</div><div class="grow"></div><button class="btn sm" id="srvCsv">' +
        icon("download") +
        'CSV</button></div><div id="srvTable"></div></div>';
      $("srvFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (!b) return;
        S.filters.servers = b.dataset.f;
        this.update();
      };
      $("srvCsv").onclick = () =>
        SG.csv("sessionguard-server.csv", [
          { label: "Server", csv: (r) => r.name },
          { label: "Status", csv: (r) => (r.online ? "online" : "offline") },
          { label: "Modus", csv: (r) => r.maintenance_mode || "online" },
          { label: "Health", csv: (r) => ((r.snapshot || {}).health || {}).score },
          { label: "CPU %", csv: (r) => Number(((r.snapshot || {}).server || {}).cpu_percent || 0).toFixed(1) },
          { label: "RAM %", csv: (r) => memPct((r.snapshot || {}).server).toFixed(1) },
          { label: "Aktive Sitzungen", csv: (r) => r.active_sessions },
          { label: "Sitzungen gesamt", csv: (r) => r.total_sessions },
          { label: "Agent-Version", csv: (r) => (r.snapshot || {}).agent_version },
          { label: "Letzter Heartbeat", csv: (r) => r.last_seen },
        ], this.rows());
    },
    rows() {
      const f = S.filters.servers;
      return S.agents.filter((a) => f === "all" || (f === "online" && a.online) || (f === "offline" && !a.online) || (a.maintenance_mode || "online") === f && (f === "drain" || f === "maintenance"));
    },
    update() {
      qsa("#srvFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.servers));
      $("srvTable").innerHTML = SG.table({
        id: "servers",
        rows: this.rows(),
        empty: "Keine Server für diesen Filter.",
        emptyIcon: "server",
        search: (r) => [r.name, r.id, (r.snapshot || {}).agent_version, ((r.snapshot || {}).server || {}).os, SG.kvText(r.tags)].join(" "),
        rowAttrs: (r) => 'class="clickable" data-href="#/servers/' + esc(encodeURIComponent(r.id)) + '"',
        defaultSort: "name",
        columns: [
          { key: "online", label: "Status", html: (r) => onlineBadge(r.online), value: (r) => (r.online ? 0 : 1) },
          { key: "name", label: "Server", html: (r) => '<strong>' + esc(r.name) + '</strong><div class="small muted">' + esc(((r.snapshot || {}).server || {}).os || "") + " · Agent " + esc((r.snapshot || {}).agent_version || "–") + "</div>" },
          { key: "mode", label: "Modus", html: (r) => modeBadge(r.maintenance_mode) + (r.restart_when_drained ? ' <span class="badge warn">Neustart wenn leer</span>' : ""), value: (r) => r.maintenance_mode || "online" },
          { key: "health", label: "Health", html: (r) => SG.healthMeter(((r.snapshot || {}).health || {}).score), value: (r) => ((r.snapshot || {}).health || {}).score || 0 },
          { key: "cpu", label: "CPU", html: (r) => SG.meter("", ((r.snapshot || {}).server || {}).cpu_percent), value: (r) => ((r.snapshot || {}).server || {}).cpu_percent || 0 },
          { key: "ram", label: "RAM", html: (r) => SG.meter("", memPct((r.snapshot || {}).server)), value: (r) => memPct((r.snapshot || {}).server) },
          { key: "sessions", label: "Sitzungen", cls: "num", html: (r) => "<strong>" + (r.active_sessions || 0) + '</strong> <span class="muted">/ ' + (r.total_sessions || 0) + "</span>", value: (r) => r.total_sessions || 0 },
          { key: "jobs", label: "Jobs", cls: "num", html: (r) => ((r.snapshot || {}).profile_jobs || []).length + " P · " + ((r.snapshot || {}).pending_cleanup || []).length + " C", value: (r) => ((r.snapshot || {}).profile_jobs || []).length },
          { key: "last_seen", label: "Heartbeat", html: (r) => SG.time(r.last_seen), value: (r) => r.last_seen || "" },
        ],
      });
    },
  };

  // ------------------------------------------------------- server detail --
  const TABS = [
    ["overview", "Übersicht", "activity"],
    ["sessions", "Sitzungen", "users"],
    ["apps", "Apps & Prozesse", "app"],
    ["profile", "Profile & Ereignisse", "box"],
    ["control", "Steuerung", "sliders"],
    ["policy", "Policy", "file"],
  ];
  views.server = {
    title: "Server",
    interval: REFRESH_MS,
    async load() {
      const id = S.route.params[0];
      const [rec, eff] = await Promise.all([api("/api/v1/agents/" + encodeURIComponent(id)), api("/api/v1/agents/" + encodeURIComponent(id) + "/policy/effective").catch(() => null)]);
      const d = S.detail;
      if (d && d.id === id) {
        d.rec = rec;
        d.eff = eff;
      }
      if (can("policy") && (!S.policyHistory || Date.now() - (S.loadedAt.ph || 0) > SLOW_MS)) {
        S.policyHistory = (await api("/api/v1/policy/history").catch(() => ({ history: [] }))).history || [];
        S.loadedAt.ph = Date.now();
      }
    },
    mount() {
      const id = S.route.params[0];
      if (!S.detail || S.detail.id !== id) S.detail = { id, rec: null, eff: null, editor: null, controlDirty: false, mountedPolicy: false };
      S.detail.tab = TABS.some((t) => t[0] === S.route.params[1]) ? S.route.params[1] : "overview";
      ["dSessions", "dProcs", "dEvents2"].forEach((t) => SG.onTable(t, () => this.update()));
      $("view").innerHTML =
        '<div id="srvHead"></div><div class="tabs" role="tablist">' +
        TABS.map(([k, l, ic]) => '<button class="tab" role="tab" data-tab="' + k + '">' + icon(ic) + esc(l) + '<span class="count" data-tabcount="' + k + '" hidden></span></button>').join("") +
        "</div>" +
        TABS.map(([k]) => '<section class="pane" data-pane="' + k + '" id="pane-' + k + '"></section>').join("");
      // Static skeletons: toolbars/search inputs are rendered once so live
      // refreshes only replace the data containers (no focus loss).
      $("pane-overview").innerHTML =
        '<div class="grid grid-3"><div class="card"><div class="card-head"><h3>Auslastung</h3><span class="small muted" id="ovSpan"></span></div><div class="card-body stack" id="ovLoad"></div></div><div class="card"><div class="card-head"><h3>System</h3></div><div class="card-body" id="ovSys"></div></div><div class="card"><div class="card-head"><h3>Health-Checks</h3></div><div class="card-body" id="ovChecks"></div></div></div><div class="grid grid-2" style="margin-top:16px"><div class="card"><div class="card-head"><h3>Ausstehende Befehle</h3><span class="small muted">vom Agent noch nicht bestätigt</span></div><div class="card-body flush" id="ovPending"></div></div><div class="card"><div class="card-head"><h3>Logon-Performance</h3></div><div class="card-body flush" id="ovTele"></div></div></div>';
      $("pane-sessions").innerHTML =
        '<div id="sesBanner"></div><div class="card"><div class="toolbar">' +
        SG.searchBox("dSessions", "Benutzer, Client, IP…") +
        '<div class="grow"></div><button class="btn sm" data-act="broadcast"' +
        noPerm("session") +
        ">" +
        icon("message") +
        'Nachricht an alle</button><button class="btn sm danger" data-act="logoff-disconnected"' +
        noPerm("session_logoff") +
        ">" +
        icon("logout") +
        'Getrennte abmelden</button></div><div id="sesList"></div></div>';
      $("pane-apps").innerHTML =
        '<div class="stack"><div class="card"><div class="card-head"><h3>RemoteApps</h3></div><div class="card-body flush" id="appList"></div></div><div class="card"><div class="card-head"><div><h3>Prozesse in Benutzersitzungen</h3><div class="hint" id="procCount"></div></div></div><div class="toolbar">' +
        SG.searchBox("dProcs", "Prozess, Benutzer, PID…") +
        '</div><div id="procList"></div></div></div>';
      $("pane-profile").innerHTML =
        '<div class="grid grid-2"><div class="card"><div class="card-head"><h3>Profil-Pipeline</h3></div><div class="card-body flush" id="profList"></div></div><div class="card"><div class="card-head"><h3>Agent-Ereignisse</h3><div class="btn-group" id="evFilter">' +
        [
          ["all", "Alle"],
          ["error", "Fehler"],
          ["warning", "Warnung"],
          ["info", "Info"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-ev="' + k + '">' + l + "</button>")
          .join("") +
        '</div></div><div class="toolbar">' +
        SG.searchBox("dEvents2", "Meldung, Benutzer…") +
        '</div><div id="evList" style="--table-max:520px"></div></div></div>';
      $("evFilter").onclick = (e) => {
        const b = e.target.closest("[data-ev]");
        if (b) {
          S.filters.events = b.dataset.ev;
          this.update();
        }
      };
      qs(".tabs", $("view")).onclick = (e) => {
        const b = e.target.closest("[data-tab]");
        if (!b) return;
        S.detail.tab = b.dataset.tab;
        history.replaceState(null, "", "#/servers/" + encodeURIComponent(id) + "/" + b.dataset.tab);
        S.lastHash = location.hash;
        this.showTab();
      };
      this.showTab();
      $("view").addEventListener("click", (e) => this.onClick(e));
    },
    showTab() {
      qsa("[data-tab]").forEach((b) => b.classList.toggle("active", b.dataset.tab === S.detail.tab));
      qsa("[data-pane]").forEach((p) => p.classList.toggle("active", p.dataset.pane === S.detail.tab));
    },
    async onClick(e) {
      const b = e.target.closest("button[data-act]");
      if (!b || b.disabled) return;
      const d = S.detail,
        a = d.rec,
        act = b.dataset.act;
      try {
        if (act === "session") await sessionAction(a.id, +b.dataset.sid, b.dataset.action, b.dataset.label);
        else if (act === "broadcast") await bulkAction(a.id, "message", "all");
        else if (act === "logoff-disconnected") await bulkAction(a.id, "logoff", "disconnected");
        else if (act === "mode") {
          const mode = b.dataset.mode;
          if (mode === (a.maintenance_mode || "online")) return;
          if (mode !== "online" && !(await SG.confirm({ title: "Modus ändern", message: a.name + " auf „" + mode + "“ setzen? " + (mode === "drain" ? "Neue Sitzungen werden nicht mehr gebrokert, bestehende bleiben erhalten." : "Der Server wird nicht mehr gebrokert, auch nicht für bestehende Sitzungen."), confirm: "Übernehmen", danger: mode === "maintenance" }))) return;
          await saveControl(a.id, { mode, restart_when_drained: !!a.restart_when_drained, tags: a.tags || {}, farm_ids: a.farm_ids || [] }, "Modus auf " + mode + " gesetzt");
          this.mountControl(true);
        } else if (act === "kill") {
          if (!(await SG.confirm({ title: "Prozess beenden", message: "Prozess " + b.dataset.name + " (PID " + b.dataset.pid + ") wirklich beenden? Nicht gespeicherte Daten des Benutzers gehen verloren.", confirm: "Beenden", danger: true }))) return;
          await api("/api/v1/agents/" + encodeURIComponent(a.id) + "/processes/" + b.dataset.pid + "/kill", { method: "POST", body: {} });
          SG.toast("Prozess-Beenden an den Agent gesendet");
        } else if (act === "save-control") await this.saveControlForm();
        else if (act === "reset-control") this.mountControl(true);
        else if (act === "save-policy") await this.savePolicy(false);
        else if (act === "save-global") await this.savePolicy(true);
        else if (act === "reload-policy") {
          if (d.editor && d.editor.isDirty() && !(await SG.confirm({ message: "Ungespeicherte Änderungen verwerfen und Policy neu laden?", confirm: "Verwerfen", danger: true }))) return;
          d.mountedPolicy = false;
          SG.setDirty("policy:" + a.id, false);
          await tick(true);
        } else if (act === "clear-override") {
          if (!(await SG.confirm({ title: "Server-Override entfernen", message: "Der Server übernimmt danach wieder die Farm- bzw. globale Policy.", confirm: "Override entfernen", danger: true }))) return;
          const r = await api("/api/v1/agents/" + encodeURIComponent(a.id) + "/policy", { method: "DELETE" });
          SG.toast("Override entfernt – Quelle jetzt: " + sourceLabel(r.source));
          d.mountedPolicy = false;
          SG.setDirty("policy:" + a.id, false);
          await tick(true);
        } else if (act === "rollback") await rollbackPolicy(b.dataset.target, b.dataset.revision, () => (d.mountedPolicy = false));
        else if (act === "view-rev") viewRevision(b.dataset.target, b.dataset.revision);
      } catch (err) {
        SG.fail(err);
      }
    },
    update() {
      const d = S.detail;
      if (!d || !d.rec) {
        $("srvHead").innerHTML = '<div class="card"><div class="card-body"><div class="skeleton" style="width:40%"></div></div></div>';
        return;
      }
      const a = d.rec,
        s = a.snapshot || {},
        v = s.server || {},
        h = s.health || {};
      const dashRow = agentById(a.id) || {};
      const online = dashRow.online;
      setCrumbs([{ label: "Terminalserver", href: "#/servers" }, { label: a.name || a.id }]);
      const modes = ["online", "drain", "maintenance"];
      $("srvHead").innerHTML =
        '<div class="card" style="margin-bottom:16px"><div class="card-body"><div class="row between" style="align-items:flex-start"><div><div class="row"><h2 style="margin:0;font-size:20px">' +
        esc(a.name) +
        "</h2>" +
        onlineBadge(online) +
        (a.maintenance_mode && a.maintenance_mode !== "online" ? modeBadge(a.maintenance_mode) : "") +
        (a.restart_when_drained ? SG.badge("Neustart wenn leer", "warn") : "") +
        '</div><div class="small muted" style="margin-top:4px">' +
        esc(v.os || "–") +
        (v.version ? " " + esc(v.version) : "") +
        " · Agent " +
        esc(s.agent_version || "–") +
        " · Heartbeat " +
        SG.time(a.last_seen) +
        ' · ID <code>' +
        esc(a.id) +
        '</code></div></div><div class="row"><div class="btn-group" title="Broker-Modus">' +
        modes.map((m) => '<button class="btn sm' + ((a.maintenance_mode || "online") === m ? " active" : "") + '" data-act="mode" data-mode="' + m + '"' + noPerm("maintenance") + ">" + (m === "online" ? "Online" : m === "drain" ? "Drain" : "Maintenance") + "</button>").join("") +
        '</div><button class="btn sm" data-act="broadcast"' +
        noPerm("session") +
        ">" +
        icon("message") +
        'An alle</button><button class="btn sm danger" data-act="logoff-disconnected"' +
        noPerm("session_logoff") +
        ">" +
        icon("logout") +
        "Getrennte abmelden</button></div></div></div></div>";
      const userSessions = (s.sessions || []).filter((x) => x.user);
      const cnt = (k, n) => {
        const el = qs('[data-tabcount="' + k + '"]');
        if (el) {
          el.textContent = n;
          el.hidden = !n;
        }
      };
      cnt("sessions", userSessions.length);
      cnt("apps", (s.remote_apps || []).length);
      cnt("profile", (s.profile_jobs || []).length);

      // overview
      const m = S.metrics[a.id] || { cpu: [], ram: [] };
      const checks = (h.checks || []).length
        ? h.checks
        : [
            { name: "RDP-Listener", ok: h.rdp_listener_ok },
            { name: "Profil-Store", ok: h.profile_store_ok },
          ];
      $("ovSpan").textContent = m.cpu.length > 1 ? "Verlauf seit " + Math.max(1, Math.round((m.cpu.length * REFRESH_MS) / 60000)) + " min" : "";
      $("ovLoad").innerHTML =
        SG.healthMeter(h.score) +
        SG.meter("CPU", v.cpu_percent, Number(v.cpu_percent || 0).toFixed(1) + " %") +
        SG.spark(m.cpu) +
        SG.meter("RAM", memPct(v), memPct(v).toFixed(1) + " % · " + SG.bytes(v.memory_available) + " frei") +
        SG.spark(m.ram, "ram") +
        (v.disk_total ? SG.meter("Systemdisk", diskPct(v), SG.bytes(v.disk_free) + " frei", [80, 92]) : '<div class="small muted">Systemdisk: ' + SG.bytes(v.disk_free) + " frei</div>");
      $("ovSys").innerHTML =
        '<div class="kv">' +
        [
          ["Uptime", v.uptime_seconds ? SG.duration(v.uptime_seconds) : "–"],
          ["RAM gesamt", SG.bytes(v.memory_total)],
          ["Broker-Score", String(Math.round(dashRow.broker_score ?? 0))],
          ["Sitzungen", (dashRow.active_sessions || 0) + " aktiv / " + (dashRow.total_sessions || 0)],
          ["Farms", (a.farm_ids || []).map((f) => (farmById(f) || {}).name || f).join(", ") || "–"],
          ["Registriert", SG.when(a.enrolled_at)],
        ]
          .map(([k, val]) => '<div><div class="k">' + esc(k) + '</div><div class="v" title="' + esc(val) + '">' + esc(val) + "</div></div>")
          .join("") +
        '</div><div class="row" style="margin-top:12px;gap:4px">' +
        (Object.entries(a.tags || {})
          .map(([k, val]) => SG.badge(k + "=" + val))
          .join("") || '<span class="small muted">Keine Tags</span>') +
        "</div>";
      $("ovChecks").innerHTML =
        '<div class="list">' +
        checks
          .map(
            (c) =>
              '<div class="row between"><span>' +
              icon(c.ok ? "check" : "alert", c.ok ? "good" : "bad") +
              " " +
              esc(c.name) +
              "</span>" +
              (c.message ? '<span class="small muted ellipsis" style="max-width:60%" title="' + esc(c.message) + '">' + esc(c.message) + "</span>" : SG.badge(c.ok ? "OK" : "Fehler", c.ok ? "ok" : "bad")) +
              "</div>",
          )
          .join("") +
        "</div>";
      $("ovPending").innerHTML = SG.table({
        id: "dPending",
        rows: a.pending_commands || [],
        empty: "Keine ausstehenden Befehle.",
        emptyIcon: "check",
        foot: false,
        columns: [
          { key: "action", label: "Aktion", html: (r) => SG.badge(r.action, "info") + (r.session_id ? ' <span class="small muted">Sitzung ' + r.session_id + "</span>" : "") + (r.pid ? ' <span class="small muted">PID ' + r.pid + "</span>" : "") },
          { key: "requested_by", label: "Von", html: (r) => esc(r.requested_by || "–") },
          { key: "created_at", label: "Erstellt", html: (r) => SG.time(r.created_at) },
          { key: "expires_at", label: "Läuft ab", html: (r) => SG.time(r.expires_at) },
        ],
      });
      $("ovTele").innerHTML = telemetryTable(s);

      // sessions
      const ctl = controlEnabled(a);
      $("sesBanner").innerHTML = ctl ? "" : '<div class="banner warn">' + icon("alert") + "Sitzungssteuerung ist in der Policy dieses Servers deaktiviert (Policy → Sitzungsrichtlinie).</div>";
      $("sesList").innerHTML = SG.table({
        id: "dSessions",
        rows: s.sessions || [],
        empty: "Keine Benutzersitzungen.",
        emptyIcon: "users",
        search: (r) => [userOf(r), r.client_name, r.client_address, r.state, r.id].join(" "),
        columns: [
          { key: "id", label: "ID", cls: "num" },
          { key: "user", label: "Benutzer", html: (r) => (r.user ? "<strong>" + esc(userOf(r)) + "</strong>" : '<span class="muted">' + esc(r.station_name || "System") + "</span>"), value: userOf },
          { key: "state", label: "Status", html: (r) => stateBadge(r.state) },
          { key: "logon_at", label: "Logon", html: (r) => SG.time(r.logon_at), value: (r) => r.logon_at || "" },
          { key: "idle", label: "Idle", html: (r) => (r.disconnected_since ? '<span class="warn">getrennt ' + esc(SG.since(r.disconnected_since)) + "</span>" : esc(SG.duration(r.idle_seconds || 0))), value: (r) => r.idle_seconds || 0 },
          { key: "client", label: "Client", html: (r) => esc(r.client_name || "–") + '<div class="small muted">' + esc(r.client_address || "") + "</div>", value: (r) => r.client_name || "" },
          { key: "act", label: "", cls: "actions", sort: false, html: (r) => (ctl && r.user ? sessionButtons(a.id, r) : "") },
        ],
      });

      // apps & processes
      const procs = (s.processes || []).filter((p) => p.session_id !== 0);
      const sessUser = {};
      (s.sessions || []).forEach((x) => (sessUser[x.id] = userOf(x)));
      $("appList").innerHTML = remoteAppsTable(s);
      $("procCount").textContent = procs.length + " Prozesse";
      $("procList").innerHTML = SG.table({
        id: "dProcs",
        rows: procs,
        max: 300,
        defaultSort: "mem",
        defaultDir: -1,
        empty: "Keine Session-Prozesse erfasst.",
        search: (p) => [p.name, p.pid, sessUser[p.session_id]].join(" "),
        columns: [
          { key: "pid", label: "PID", cls: "num" },
          { key: "name", label: "Prozess", html: (p) => "<strong>" + esc(p.name) + "</strong>" },
          { key: "session_id", label: "Sitzung", html: (p) => esc(p.session_id) + '<div class="small muted">' + esc(sessUser[p.session_id] || "") + "</div>" },
          { key: "mem", label: "RAM", cls: "num", html: (p) => esc(SG.bytes(p.memory_bytes)), value: (p) => p.memory_bytes || 0 },
          { key: "act", label: "", cls: "actions", sort: false, html: (p) => '<button class="btn sm danger" data-act="kill" data-pid="' + p.pid + '" data-name="' + esc(p.name) + '"' + noPerm("process") + ">" + icon("x") + "Beenden</button>" },
        ],
      });

      // profiles & events
      const evf = S.filters.events;
      qsa("#evFilter [data-ev]").forEach((b) => b.classList.toggle("active", b.dataset.ev === evf));
      const events = (s.events || [])
        .slice()
        .reverse()
        .filter((x) => evf === "all" || (x.level || "info") === evf);
      $("profList").innerHTML = profileTables(s);
      $("evList").innerHTML = SG.table({
        id: "dEvents2",
        rows: events.slice(0, 300),
        empty: "Keine Ereignisse.",
        search: (r) => [r.message, r.user, r.level].join(" "),
        rowAttrs: (r) => 'class="lvl-' + esc(r.level || "info") + '"',
        columns: [
          { key: "time", label: "Zeit", html: (r) => SG.time(r.time), value: (r) => r.time || "" },
          { key: "level", label: "Typ", html: (r) => SG.badge(r.level || "info", r.level === "error" ? "bad" : r.level === "warning" || r.level === "dry-run" ? "warn" : "") },
          { key: "user", label: "Benutzer", html: (r) => esc(r.user || "–") },
          { key: "message", label: "Meldung", html: (r) => esc(r.message) },
        ],
      });

      this.mountControl(false);
      this.mountPolicy();
    },

    mountControl(force) {
      const d = S.detail,
        a = d.rec;
      const pane = $("pane-control");
      if (!force && d.controlDirty && pane.dataset.agent === a.id) return;
      if (!force && pane.dataset.agent === a.id && pane.dataset.rev === controlRev(a)) return;
      pane.dataset.agent = a.id;
      pane.dataset.rev = controlRev(a);
      d.controlDirty = false;
      SG.setDirty("control:" + a.id, false);
      const known = S.farms.map((f) => f.id);
      const extra = (a.farm_ids || []).filter((f) => !known.includes(f));
      pane.innerHTML =
        '<div class="card"><div class="card-head"><div><h3>Serversteuerung</h3><div class="hint">Broker-Modus, Farm-Zuordnung und Tags. Änderungen wirken beim nächsten Broker-Request bzw. Heartbeat.</div></div><span class="dirty" id="ctlDirty" hidden>' +
        icon("edit") +
        'Ungespeichert</span></div><div class="card-body form"><div class="fields"><label class="field"><span>Broker-Modus</span><select id="ctlMode">' +
        [
          ["online", "Online – normal brokern"],
          ["drain", "Drain – nur bestehende Sitzungen"],
          ["maintenance", "Maintenance – nicht brokern"],
        ]
          .map(([k, l]) => '<option value="' + k + '"' + ((a.maintenance_mode || "online") === k ? " selected" : "") + ">" + l + "</option>")
          .join("") +
        '</select></label><div class="field"><span>&nbsp;</span><label class="switch"><input type="checkbox" id="ctlRestart"' +
        (a.restart_when_drained ? " checked" : "") +
        '> Neu starten, sobald keine Benutzer mehr angemeldet sind</label></div></div><div class="fields"><div class="field"><span>Farm-Zuordnung</span><div class="list" style="gap:6px">' +
        (S.farms.length
          ? S.farms.map((f) => '<label class="switch"><input type="checkbox" data-farm="' + esc(f.id) + '"' + ((a.farm_ids || []).includes(f.id) ? " checked" : "") + "> " + esc(f.name) + ' <span class="small muted">' + esc(f.id) + "</span></label>").join("")
          : '<span class="help">Noch keine Farms angelegt.</span>') +
        '</div><small>Zusätzlich können Farms Server über Required Tags oder eine explizite Mitgliederliste einschließen.</small></div><label class="field"><span>Weitere Farm-IDs (eine pro Zeile)</span><textarea id="ctlFarmsExtra" rows="3">' +
        esc(extra.join("\n")) +
        '</textarea></label><label class="field"><span>Tags (key=value, eine pro Zeile)</span><textarea id="ctlTags" rows="4">' +
        esc(SG.kvText(a.tags)) +
        '</textarea></label></div></div><div class="card-foot"><button class="btn" data-act="reset-control">' +
        icon("undo") +
        'Zurücksetzen</button><button class="btn primary" data-act="save-control"' +
        noPerm("maintenance") +
        ">" +
        icon("check") +
        "Speichern</button></div></div>";
      pane.oninput = pane.onchange = () => {
        d.controlDirty = true;
        SG.setDirty("control:" + a.id, true);
        $("ctlDirty").hidden = false;
      };
    },
    async saveControlForm() {
      const d = S.detail,
        a = d.rec;
      const farms = qsa("[data-farm]", $("pane-control"))
        .filter((x) => x.checked)
        .map((x) => x.dataset.farm)
        .concat(SG.lines($("ctlFarmsExtra").value));
      const body = { mode: $("ctlMode").value, restart_when_drained: $("ctlRestart").checked, tags: SG.parseKV($("ctlTags").value), farm_ids: Array.from(new Set(farms)) };
      await saveControl(a.id, body);
      d.controlDirty = false;
      SG.setDirty("control:" + a.id, false);
      this.mountControl(true);
    },

    mountPolicy() {
      const d = S.detail,
        a = d.rec,
        eff = d.eff;
      const pane = $("pane-policy");
      const src = eff ? eff.source : a.desired_policy ? "agent" : "none";
      const effRev = eff && eff.policy ? eff.policy.revision : "";
      const reported = (a.snapshot || {}).policy_revision || (eff && eff.reported_revision) || "";
      const synced = effRev && reported === effRev;
      const info =
        '<div class="note' +
        (src === "agent" ? " warn" : "") +
        '"><div class="row between"><div><strong>Quelle der aktiven Policy:</strong> ' +
        esc(sourceLabel(src, eff && eff.farm_name)) +
        (effRev ? ' · Revision <code>' + esc(effRev) + "</code> " + (synced ? SG.badge("vom Agent übernommen", "ok") : SG.badge("Übernahme ausstehend", "warn")) : "") +
        (src === "none" ? " – der Agent nutzt seine lokale Policy aus agent.json." : "") +
        "</div>" +
        (src === "agent" ? '<button class="btn sm danger" data-act="clear-override"' + noPerm("policy") + ">" + icon("undo") + "Override entfernen</button>" : "") +
        "</div></div>";
      if (!pane.dataset.mounted || pane.dataset.agent !== a.id || !d.mountedPolicy) {
        pane.dataset.agent = a.id;
        pane.dataset.mounted = "1";
        d.mountedPolicy = true;
        const base = a.desired_policy && a.desired_policy.revision ? "Server-Override (Master)" : "vom Agent gemeldete Ist-Policy";
        pane.innerHTML =
          '<div class="stack"><div id="polInfo"></div><div class="card"><div class="card-head"><div><h3>Policy bearbeiten</h3><div class="hint">Editor-Basis: ' +
          esc(base) +
          '. Live-Refresh verändert offene Eingaben nicht.</div></div><span class="dirty" id="polDirty" hidden>' +
          icon("edit") +
          'Ungespeichert</span></div><div class="card-body" id="polEditor"></div><div class="card-foot"><button class="btn" data-act="reload-policy">' +
          icon("refresh") +
          'Neu laden</button><button class="btn" data-act="save-global"' +
          noPerm("policy") +
          ' title="Setzt die globale Standard-Policy. Server mit eigenem Override oder Farm-Policy behalten diese.">' +
          icon("layers") +
          'Als globale Policy speichern</button><button class="btn primary" data-act="save-policy"' +
          noPerm("policy") +
          ">" +
          icon("check") +
          'Für diesen Server speichern</button></div></div><div class="card"><div class="card-head"><h3>Versionen dieses Servers</h3></div><div class="card-body flush" id="polHistory"></div></div></div>';
        d.editor = SG.PolicyEditor($("polEditor"), activePolicy(a), {
          onDirty: (v) => {
            $("polDirty").hidden = !v;
            SG.setDirty("policy:" + a.id, v);
          },
        });
      }
      $("polInfo").innerHTML = info;
      const rows = (S.policyHistory || []).filter((x) => x.target === "agent:" + a.id).reverse();
      $("polHistory").innerHTML = can("policy") ? policyHistoryTable("dPolHist", rows) : SG.empty("Policy-Historie erfordert die Rolle Policy-Admin oder Admin.", "shield");
    },
    async savePolicy(all) {
      const d = S.detail,
        a = d.rec;
      const p = d.editor.collect();
      if (all && !(await SG.confirm({ title: "Globale Policy setzen", message: "Diese Policy wird zur globalen Standard-Policy. Sie gilt für alle Server ohne eigenen Override und ohne Farm-Policy.", confirm: "Global speichern" }))) return;
      await api(all ? "/api/v1/policy/all" : "/api/v1/agents/" + encodeURIComponent(a.id) + "/policy", { method: "PUT", body: p });
      SG.toast(all ? "Globale Policy gespeichert" : "Policy für diesen Server gespeichert");
      d.editor.markClean();
      d.mountedPolicy = false;
      S.policyHistory = null;
      await tick(true);
    },
    leave() {
      if (S.detail && S.detail.rec) {
        SG.setDirty("policy:" + S.detail.rec.id, false);
        SG.setDirty("control:" + S.detail.rec.id, false);
      }
    },
  };
  const controlRev = (a) => JSON.stringify([a.maintenance_mode, a.restart_when_drained, a.tags, a.farm_ids]);
  const sourceLabel = (src, farmName) =>
    src === "agent" ? "Server-Override" : src === "global" ? "Globale Policy" : src === "none" ? "Keine Master-Policy" : src && src.startsWith("farm:") ? "Farm " + (farmName || (farmById(src.slice(5)) || {}).name || src.slice(5)) : src || "–";

  function activePolicy(a) {
    if (a.desired_policy && a.desired_policy.revision) return a.desired_policy;
    if (a.snapshot && a.snapshot.policy) return a.snapshot.policy;
    return {
      cleanup: { grace_seconds: 600, poll_seconds: 10, retry_seconds: 60, dry_run: true, allowed_profile_roots: ["C:\\Users"] },
      profiles: { retry_seconds: 60, keep_versions: 2, folders: [] },
      sessions: { disconnected_timeout_seconds: 3600 },
      templates: [],
    };
  }

  function sessionButtons(agentId, r) {
    const label = userOf(r) + " (Sitzung " + r.id + ")";
    const base = ' data-act="session" data-sid="' + r.id + '" data-label="' + esc(label) + '"';
    return (
      '<button class="btn sm icon" title="Nachricht"' +
      base +
      ' data-action="message"' +
      noPerm("session") +
      ">" +
      icon("message") +
      '</button><button class="btn sm icon" title="Trennen"' +
      base +
      ' data-action="disconnect"' +
      noPerm("session") +
      ">" +
      icon("unplug") +
      '</button><button class="btn sm icon danger" title="Abmelden"' +
      base +
      ' data-action="logoff"' +
      noPerm("session_logoff") +
      ">" +
      icon("logout") +
      "</button>"
    );
  }

  function telemetryTable(s) {
    const rows = Object.values(s.telemetry || {})
      .sort((x, y) => new Date(y.first_seen_at) - new Date(x.first_seen_at))
      .slice(0, 100);
    return SG.table({
      id: "dTele",
      rows,
      empty: "Noch keine Logon-Telemetrie.",
      foot: false,
      columns: [
        { key: "session_id", label: "Sitzung", cls: "num" },
        { key: "user", label: "Benutzer", html: (x) => esc(x.user || x.sid || "–") },
        { key: "logon", label: "Logon", html: (x) => SG.time(x.logon_at || x.first_seen_at), value: (x) => x.logon_at || x.first_seen_at },
        { key: "restore", label: "Restore", cls: "num", html: (x) => ((x.restore_duration_ms || 0) / 1000).toFixed(2) + " s", value: (x) => x.restore_duration_ms || 0 },
        { key: "ready", label: "Bis Ready", cls: "num", html: (x) => '<span class="' + ((x.observed_logon_ms || 0) > 30000 ? "warn" : "") + '">' + ((x.observed_logon_ms || 0) / 1000).toFixed(2) + " s</span>", value: (x) => x.observed_logon_ms || 0 },
      ],
    });
  }

  function remoteAppsTable(s) {
    return SG.table({
      id: "dApps",
      rows: s.remote_apps || [],
      empty: "Keine RemoteApps entdeckt oder verwaltet.",
      emptyIcon: "app",
      foot: false,
      columns: [
        { key: "ok", label: "Status", html: (x) => (x.published && x.path_exists && x.in_sync ? SG.badge("Bereit", "ok") : SG.badge("Nicht bereit", "bad")), value: (x) => (x.published && x.path_exists && x.in_sync ? 0 : 1) },
        { key: "alias", label: "Alias", html: (x) => "<strong>||" + esc(x.alias) + '</strong><div class="small muted">' + esc(x.display_name || x.resource_id || "–") + "</div>" },
        { key: "path", label: "Pfad", html: (x) => '<span class="mono">' + esc(x.path || "–") + "</span>" },
        { key: "managed", label: "Quelle", html: (x) => (x.managed ? SG.badge("SessionGuard", "info") + ' <span class="small muted">' + (x.owned ? "angelegt" : "übernommen") + "</span>" : SG.badge("entdeckt")) },
        { key: "error", label: "Fehler", html: (x) => (x.error ? '<span class="bad">' + esc(x.error) + "</span>" : "–") },
      ],
    });
  }

  function profileTables(s) {
    const jobs = s.profile_jobs || [],
      st = Object.values(s.profile_status || {});
    if (!jobs.length && !st.length) return SG.empty("Keine Profil-Jobs oder Statusdaten.", "box");
    return (
      (jobs.length
        ? SG.table({
            id: "dJobs",
            rows: jobs,
            foot: false,
            columns: [
              { key: "operation", label: "Operation", html: (j) => SG.badge(j.operation, "info") },
              { key: "user", label: "Benutzer" },
              { key: "due_at", label: "Fällig", html: (j) => SG.time(j.due_at) },
              { key: "attempts", label: "Versuche", cls: "num", html: (j) => esc(j.attempts ?? 0) },
              { key: "last_error", label: "Fehler", html: (j) => (j.last_error ? '<span class="bad">' + esc(j.last_error) + "</span>" : "–") },
            ],
          })
        : "") +
      (st.length
        ? SG.table({
            id: "dProfStatus",
            rows: st,
            foot: false,
            columns: [
              { key: "user", label: "Benutzer", html: (x) => esc(x.user || x.sid) },
              { key: "last_backup_at", label: "Backup", html: (x) => SG.time(x.last_backup_at) },
              { key: "last_restore_at", label: "Restore", html: (x) => SG.time(x.last_restore_at) },
              { key: "err", label: "Status", html: (x) => (x.last_backup_error || x.last_restore_error ? '<span class="bad">' + esc(x.last_backup_error || x.last_restore_error) + "</span>" : SG.badge("OK", "ok")) },
            ],
          })
        : "")
    );
  }

  function policyHistoryTable(id, rows, withTarget) {
    return SG.table({
      id,
      rows: rows.slice(0, 250),
      empty: "Noch keine Policy-Versionen.",
      emptyIcon: "file",
      search: (x) => [policyTarget(x.target), x.revision, x.actor].join(" "),
      columns: [
        { key: "created_at", label: "Zeit", html: (x) => SG.time(x.created_at) },
        ...(withTarget ? [{ key: "target", label: "Ziel", html: (x) => esc(policyTarget(x.target)), value: (x) => policyTarget(x.target) }] : []),
        { key: "revision", label: "Revision", html: (x) => "<code>" + esc(x.revision) + "</code>" },
        { key: "actor", label: "Akteur", html: (x) => esc(x.actor || "–") },
        {
          key: "act",
          label: "",
          cls: "actions",
          sort: false,
          html: (x) =>
            '<button class="btn sm" data-act="view-rev" data-target="' +
            esc(x.target) +
            '" data-revision="' +
            esc(x.revision) +
            '">' +
            icon("eye") +
            'Anzeigen</button><button class="btn sm" data-act="rollback" data-target="' +
            esc(x.target) +
            '" data-revision="' +
            esc(x.revision) +
            '"' +
            noPerm("policy") +
            ">" +
            icon("undo") +
            "Rollback</button>",
        },
      ],
    });
  }

  async function rollbackPolicy(target, revision, after) {
    if (!(await SG.confirm({ title: "Policy-Rollback", message: policyTarget(target) + " auf Revision " + revision + " zurückrollen? Es entsteht eine neue Revision mit diesem Inhalt.", confirm: "Rollback" }))) return;
    const rev = encodeURIComponent(revision);
    let url;
    if (target === "global") url = "/api/v1/policy/global/rollback/" + rev;
    else if (target.startsWith("agent:")) url = "/api/v1/agents/" + encodeURIComponent(target.slice(6)) + "/policy/rollback/" + rev;
    else if (target.startsWith("farm:")) url = "/api/v1/farms/" + encodeURIComponent(target.slice(5)) + "/policy/rollback/" + rev;
    else throw new Error("Unbekanntes Policy-Ziel");
    await api(url, { method: "POST" });
    SG.toast("Rollback eingeplant");
    if (after) after();
    S.policyHistory = null;
    await tick(true);
  }

  function viewRevision(target, revision) {
    const v = (S.policyHistory || []).find((x) => x.target === target && x.revision === revision);
    if (!v) return SG.toast("Revision nicht gefunden", "warn");
    SG.modal({
      title: "Policy-Revision " + revision,
      subtitle: policyTarget(target) + " · " + SG.when(v.created_at) + " · " + (v.actor || "–"),
      size: "wide",
      body: SG.json(v.policy),
      actions: [{ label: "Schließen", value: null }],
    });
  }

  // ------------------------------------------------------ all sessions --
  views.sessions = {
    title: "Sitzungen",
    interval: REFRESH_MS,
    mount() {
      SG.onTable("sessions", () => this.update());
      $("view").innerHTML =
        pageHead("Sitzungen", "Alle Benutzersitzungen über alle Terminalserver. Mehrfachauswahl für Nachrichten, Trennen und Abmelden.") +
        '<div class="card"><div class="toolbar">' +
        SG.searchBox("sessions", "Benutzer, Server, Client, IP…") +
        '<div class="btn-group" id="sesFilter">' +
        [
          ["all", "Alle"],
          ["active", "Aktiv"],
          ["disconnected", "Getrennt"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-f="' + k + '">' + l + "</button>")
          .join("") +
        '</div><div class="grow"></div><span class="small muted" id="selInfo"></span><button class="btn sm" data-bulk="message"' +
        noPerm("session") +
        ">" +
        icon("message") +
        'Nachricht</button><button class="btn sm" data-bulk="disconnect"' +
        noPerm("session") +
        ">" +
        icon("unplug") +
        'Trennen</button><button class="btn sm danger" data-bulk="logoff"' +
        noPerm("session_logoff") +
        ">" +
        icon("logout") +
        'Abmelden</button><button class="btn sm" id="sesCsv">' +
        icon("download") +
        'CSV</button></div><div id="sesTable"></div></div>';
      $("sesFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (b) {
          S.filters.sessions = b.dataset.f;
          this.update();
        }
      };
      $("view").addEventListener("change", (e) => {
        if (e.target.matches("[data-sel]")) {
          e.target.checked ? S.selection.add(e.target.dataset.sel) : S.selection.delete(e.target.dataset.sel);
          this.updateSel();
        } else if (e.target.matches("[data-selall]")) {
          this.rows().forEach((r) => (e.target.checked && controlEnabled(r.agent) ? S.selection.add(r.key) : S.selection.delete(r.key)));
          this.update();
        }
      });
      $("view").addEventListener("click", async (e) => {
        const one = e.target.closest("button[data-act=session]");
        if (one && !one.disabled) return sessionAction(one.dataset.agent, +one.dataset.sid, one.dataset.action, one.dataset.label);
        const bulk = e.target.closest("button[data-bulk]");
        if (bulk && !bulk.disabled) this.bulk(bulk.dataset.bulk);
      });
      $("sesCsv").onclick = () =>
        SG.csv("sessionguard-sitzungen.csv", [
          { label: "Benutzer", csv: userOf },
          { label: "Server", csv: (r) => r.agent.name },
          { label: "Sitzung", csv: (r) => r.id },
          { label: "Status", csv: (r) => r.state },
          { label: "Logon", csv: (r) => r.logon_at },
          { label: "Idle (s)", csv: (r) => r.idle_seconds || 0 },
          { label: "Getrennt seit", csv: (r) => r.disconnected_since },
          { label: "Client", csv: (r) => r.client_name },
          { label: "Client-IP", csv: (r) => r.client_address },
        ], this.rows());
    },
    rows() {
      const f = S.filters.sessions;
      return allSessions().filter((s) => f === "all" || String(s.state).toLowerCase() === f);
    },
    updateSel() {
      const n = S.selection.size;
      $("selInfo").textContent = n ? n + " ausgewählt" : "";
      qsa("[data-bulk]").forEach((b) => {
        const need = b.dataset.bulk === "logoff" ? "session_logoff" : "session";
        b.disabled = !n || !can(need);
      });
    },
    update() {
      qsa("#sesFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.sessions));
      const live = new Set(allSessions().map((s) => s.key));
      Array.from(S.selection).forEach((k) => live.has(k) || S.selection.delete(k));
      const rows = this.rows();
      $("sesTable").innerHTML = SG.table({
        id: "sessions",
        rows,
        empty: "Keine Benutzersitzungen.",
        emptyIcon: "users",
        defaultSort: "user",
        search: (r) => [userOf(r), r.agent.name, r.client_name, r.client_address, r.state].join(" "),
        rowAttrs: (r) => (S.selection.has(r.key) ? 'class="selected"' : ""),
        columns: [
          {
            key: "sel",
            label: "",
            labelHtml: '<input type="checkbox" class="checkbox" data-selall aria-label="Alle auswählen">',
            cls: "check-col",
            sort: false,
            html: (r) => (controlEnabled(r.agent) ? '<input type="checkbox" class="checkbox" data-sel="' + esc(r.key) + '"' + (S.selection.has(r.key) ? " checked" : "") + ' aria-label="Auswählen">' : '<span title="Sitzungssteuerung in der Policy deaktiviert">–</span>'),
          },
          { key: "user", label: "Benutzer", html: (r) => "<strong>" + esc(userOf(r)) + "</strong>", value: userOf },
          { key: "server", label: "Server", html: (r) => link("servers/" + encodeURIComponent(r.agent.id) + "/sessions", esc(r.agent.name)) + (r.agent.online ? "" : " " + SG.badge("offline")), value: (r) => r.agent.name },
          { key: "state", label: "Status", html: (r) => stateBadge(r.state) },
          { key: "logon_at", label: "Logon", html: (r) => SG.time(r.logon_at), value: (r) => r.logon_at || "" },
          { key: "idle", label: "Idle", html: (r) => (r.disconnected_since ? '<span class="warn">getrennt ' + esc(SG.since(r.disconnected_since)) + "</span>" : esc(SG.duration(r.idle_seconds || 0))), value: (r) => r.idle_seconds || 0 },
          { key: "client", label: "Client", html: (r) => esc(r.client_name || "–") + '<div class="small muted">' + esc(r.client_address || "") + "</div>", value: (r) => r.client_name || "" },
          {
            key: "act",
            label: "",
            cls: "actions",
            sort: false,
            html: (r) => (controlEnabled(r.agent) ? sessionButtons(r.agent.id, r).replace(/data-act="session"/g, 'data-act="session" data-agent="' + esc(r.agent.id) + '"') : ""),
          },
        ],
      });
      this.updateSel();
    },
    async bulk(action) {
      const targets = allSessions().filter((s) => S.selection.has(s.key));
      if (!targets.length) return;
      const body = { action };
      if (action === "message") {
        const m = await SG.prompt({ title: "Nachricht an " + targets.length + " Sitzungen", label: "Nachricht", multiline: true, confirm: "Senden", icon: "message" });
        if (m === null) return;
        body.message = m;
        body.title = "SessionGuard";
      } else if (!(await SG.confirm({ title: action === "logoff" ? "Sitzungen abmelden" : "Sitzungen trennen", message: targets.length + " ausgewählte Sitzung(en) " + (action === "logoff" ? "abmelden? Die Profilsicherung startet nach dem Sitzungsende." : "trennen?"), confirm: action === "logoff" ? "Abmelden" : "Trennen", danger: action === "logoff" }))) return;
      let ok = 0,
        fail = 0;
      for (const t of targets) {
        try {
          await api("/api/v1/agents/" + encodeURIComponent(t.agent.id) + "/sessions/" + t.id + "/action", { method: "POST", body });
          ok++;
        } catch (e) {
          fail++;
        }
      }
      S.selection.clear();
      SG.toast(ok + " Aktion(en) eingeplant" + (fail ? ", " + fail + " fehlgeschlagen" : ""), fail ? "warn" : "ok");
      setTimeout(() => tick(true), 1200);
    },
  };

  // -------------------------------------------------------------- farms --
  views.farms = {
    title: "Farms & Broker",
    interval: REFRESH_MS,
    async load() {
      const [fr, rr, lr] = await Promise.all([api("/api/v1/farms"), api("/api/v1/resources"), api("/api/v1/leases")]);
      S.farms = fr.farms || [];
      S.resources = rr.resources || [];
      S.leases = lr.leases || [];
    },
    mount() {
      SG.onTable("leases", () => this.update());
      $("view").innerHTML =
        pageHead(
          "Farms & Broker",
          "Server gruppieren (explizit, über die Serversteuerung oder per Required Tags), Farm-Policies pflegen und aktive Broker-Leases nachvollziehen.",
          '<button class="btn primary" data-farm-act="new"' + noPerm("manage") + ">" + icon("plus") + "Farm anlegen</button>",
        ) +
        '<div class="grid grid-auto" id="farmCards"></div><div class="card" style="margin-top:16px"><div class="card-head"><div><h3>Aktive Broker-Leases</h3><div class="hint">Reconnect-Affinität: Benutzer → Server</div></div></div><div class="toolbar">' +
        SG.searchBox("leases", "Benutzer, Server, Resource…") +
        '</div><div id="leaseTable"></div></div>';
      $("view").addEventListener("click", (e) => {
        const b = e.target.closest("[data-farm-act]");
        if (b && !b.disabled) this.act(b.dataset.farmAct, b.dataset.id).catch(SG.fail);
      });
    },
    update() {
      $("farmCards").innerHTML = S.farms.length
        ? S.farms
            .map((f) => {
              const members = farmMembers(f);
              return (
                '<div class="card entity"><div class="entity-head"><div><div class="entity-title">' +
                esc(f.name) +
                '</div><div class="small muted">' +
                esc(f.description || f.id) +
                '</div></div><div class="row" style="gap:4px">' +
                SG.badge(f.enabled === false ? "Deaktiviert" : "Aktiv", f.enabled === false ? "bad" : "ok") +
                (f.policy ? SG.badge("Farm-Policy", "info") : "") +
                '</div></div><div class="kv"><div><div class="k">Server</div><div class="v">' +
                members.length +
                '</div></div><div><div class="k">Resources</div><div class="v">' +
                S.resources.filter((r) => r.farm_id === f.id).length +
                '</div></div><div><div class="k">Leases</div><div class="v">' +
                S.leases.filter((l) => l.farm_id === f.id).length +
                '</div></div></div><div class="row" style="gap:4px">' +
                (Object.entries(f.required_tags || {})
                  .map(([k, v]) => SG.badge(k + "=" + v))
                  .join("") || '<span class="small muted">Keine Required Tags</span>') +
                '</div><div class="small">' +
                (members.length
                  ? members
                      .slice(0, 8)
                      .map((a) => link("servers/" + encodeURIComponent(a.id), esc(a.name)))
                      .join(", ") + (members.length > 8 ? " … +" + (members.length - 8) : "")
                  : '<span class="muted">Noch keine Server zugeordnet</span>') +
                '</div><div class="row end"><button class="btn sm" data-farm-act="policy" data-id="' +
                esc(f.id) +
                '">' +
                icon("file") +
                'Policy</button><button class="btn sm" data-farm-act="edit" data-id="' +
                esc(f.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("edit") +
                'Bearbeiten</button><button class="btn sm danger icon" title="Löschen" data-farm-act="delete" data-id="' +
                esc(f.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("trash") +
                "</button></div></div>"
              );
            })
            .join("")
        : '<div class="card">' + SG.empty("Noch keine Farms. Über „Farm anlegen“ entsteht die erste Broker-Gruppe.", "layers") + "</div>";
      $("leaseTable").innerHTML = SG.table({
        id: "leases",
        rows: S.leases,
        empty: "Keine aktiven Leases.",
        search: (l) => [l.user_key, agentName(l.agent_id), l.farm_id, l.resource_id, l.reason].join(" "),
        columns: [
          { key: "user_key", label: "Benutzer", html: (l) => "<strong>" + esc(l.user_key) + "</strong>" },
          { key: "agent", label: "Server", html: (l) => link("servers/" + encodeURIComponent(l.agent_id), esc(agentName(l.agent_id))), value: (l) => agentName(l.agent_id) },
          { key: "farm", label: "Farm", html: (l) => esc(((farmById(l.farm_id) || {}).name || l.farm_id) || "–"), value: (l) => l.farm_id },
          { key: "resource", label: "Resource", html: (l) => esc(((S.resources.find((r) => r.id === l.resource_id) || {}).name || l.resource_id) || "–"), value: (l) => l.resource_id },
          { key: "reason", label: "Grund", html: (l) => SG.badge(l.reason || "–", "info") },
          { key: "expires_at", label: "Gültig bis", html: (l) => SG.time(l.expires_at) },
        ],
      });
    },
    async act(act, id) {
      const f = farmById(id);
      if (act === "delete") {
        if (!(await SG.confirm({ title: "Farm löschen", message: "Farm „" + f.name + "“ löschen? Resources dieser Farm werden nicht mehr gebrokert.", confirm: "Löschen", danger: true }))) return;
        await api("/api/v1/farms/" + encodeURIComponent(id), { method: "DELETE" });
        SG.toast("Farm gelöscht");
        return tick(true);
      }
      if (act === "policy") return farmPolicyDialog(f);
      const x = act === "edit" ? f : { name: "", description: "", enabled: true, required_tags: {}, agent_ids: [] };
      await SG.modal({
        title: act === "edit" ? "Farm bearbeiten" : "Farm anlegen",
        subtitle: act === "edit" ? "ID " + x.id : "Logische Gruppe von Terminalservern für den Broker",
        size: "wide",
        body:
          '<div class="form"><div class="fields"><label class="field"><span>Name</span><input id="fName" value="' +
          esc(x.name) +
          '" placeholder="Office"></label><label class="field"><span>Beschreibung</span><input id="fDesc" value="' +
          esc(x.description || "") +
          '"></label></div><label class="switch"><input type="checkbox" id="fEnabled"' +
          (x.enabled !== false ? " checked" : "") +
          '> Farm aktiv (deaktivierte Farms werden nicht gebrokert)</label><div class="fields"><label class="field"><span>Required Tags (key=value, eine pro Zeile)</span><textarea id="fTags" rows="4">' +
          esc(SG.kvText(x.required_tags)) +
          '</textarea><small>Server mit allen diesen Tags gehören automatisch zur Farm.</small></label><div class="field"><span>Explizite Mitglieder</span><div class="list" style="gap:6px;max-height:220px;overflow:auto">' +
          (S.agents.length
            ? S.agents.map((a) => '<label class="switch"><input type="checkbox" data-member="' + esc(a.id) + '"' + ((x.agent_ids || []).includes(a.id) ? " checked" : "") + "> " + esc(a.name) + "</label>").join("")
            : '<span class="help">Keine Server registriert.</span>') +
          "</div><small>Server können sich zusätzlich über ihre Serversteuerung einer Farm zuordnen.</small></div></div></div>",
        actions: [
          { label: "Abbrechen", value: null },
          { label: act === "edit" ? "Speichern" : "Farm anlegen", kind: "primary", value: "save" },
        ],
        onAction: async () => {
          const name = $("fName").value.trim();
          if (!name) {
            SG.toast("Name ist erforderlich", "warn");
            return false;
          }
          const body = {
            name,
            description: $("fDesc").value.trim(),
            enabled: $("fEnabled").checked,
            required_tags: SG.parseKV($("fTags").value),
            agent_ids: qsa("[data-member]")
              .filter((c) => c.checked)
              .map((c) => c.dataset.member),
          };
          await api(act === "edit" ? "/api/v1/farms/" + encodeURIComponent(id) : "/api/v1/farms", { method: act === "edit" ? "PUT" : "POST", body });
          SG.toast(act === "edit" ? "Farm gespeichert" : "Farm angelegt");
          await tick(true);
        },
      });
    },
  };

  async function farmPolicyDialog(f) {
    let ed;
    let base = f.policy;
    if (!base) {
      const g = await api("/api/v1/policy/global").catch(() => ({}));
      base = g.policy || activePolicy({});
    }
    await SG.modal({
      title: "Farm-Policy · " + f.name,
      subtitle: f.policy ? "Revision " + f.policy.revision : "Noch keine Farm-Policy – Vorlage: " + (base.revision ? "globale Policy" : "Standardwerte"),
      size: "xwide",
      body: '<div class="note" style="margin-bottom:14px">Gilt für alle Server dieser Farm ohne eigenen Server-Override. Bei mehreren Farms gewinnt die zuerst in der Serversteuerung zugeordnete Farm.</div><div id="farmPolEd"></div>',
      footLeft: f.policy && can("policy") ? '<button type="button" class="btn danger" id="farmPolClear">' + icon("trash") + "Farm-Policy entfernen</button>" : "",
      actions: [
        { label: "Abbrechen", value: null },
        { label: "Farm-Policy speichern", kind: "primary", value: "save" },
      ],
      onOpen: (dlg) => {
        ed = SG.PolicyEditor($("farmPolEd"), base);
        const clr = $("farmPolClear");
        if (clr)
          clr.onclick = async () => {
            if (!(await SG.confirm({ message: "Farm-Policy entfernen? Die Server übernehmen danach die nächste passende Farm- oder die globale Policy.", confirm: "Entfernen", danger: true }))) return;
            try {
              await api("/api/v1/farms/" + encodeURIComponent(f.id) + "/policy", { method: "DELETE" });
              SG.toast("Farm-Policy entfernt");
              SG.closeDialog(dlg);
              dlg.remove();
              document.body.classList.remove("dlg-open");
              await tick(true);
            } catch (e) {
              SG.fail(e);
            }
          };
      },
      guard: () => !ed || !ed.isDirty() || confirm("Ungespeicherte Änderungen verwerfen?"),
      onAction: async () => {
        if (!can("policy")) {
          SG.toast("Fehlende Berechtigung: policy", "warn");
          return false;
        }
        await api("/api/v1/farms/" + encodeURIComponent(f.id) + "/policy", { method: "PUT", body: ed.collect() });
        SG.toast("Farm-Policy gespeichert");
        S.policyHistory = null;
        await tick(true);
      },
    });
  }

  // ---------------------------------------------------------- resources --
  views.resources = {
    title: "Apps & Desktops",
    interval: REFRESH_MS,
    load: views.farms.load,
    mount() {
      SG.onTable("resources", () => this.update());
      $("view").innerHTML =
        pageHead(
          "Apps & Desktops",
          "Published Resources verknüpfen Guacamole-Verbindungen mit Farms. RemoteApps können auf allen Farm-Servern automatisch veröffentlicht werden.",
          '<button class="btn primary" data-res-act="new"' + noPerm("manage") + ">" + icon("plus") + "Resource anlegen</button>",
        ) +
        '<div class="card" style="margin-bottom:16px"><div class="toolbar">' +
        SG.searchBox("resources", "Name, Farm, Guacamole-ID, Alias…") +
        '<div class="btn-group" id="resFilter">' +
        [
          ["all", "Alle"],
          ["desktop", "Desktops"],
          ["remoteapp", "RemoteApps"],
          ["disabled", "Deaktiviert"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-f="' + k + '">' + l + "</button>")
          .join("") +
        '</div></div></div><div class="grid grid-auto" id="resCards"></div>';
      $("resFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (b) {
          S.filters.resources = b.dataset.f;
          this.update();
        }
      };
      $("view").addEventListener("click", (e) => {
        const b = e.target.closest("[data-res-act]");
        if (b && !b.disabled) this.act(b.dataset.resAct, b.dataset.id).catch(SG.fail);
      });
    },
    update() {
      qsa("#resFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.resources));
      const f = S.filters.resources,
        q = SG.tstate("resources").q.toLowerCase();
      const rows = S.resources
        .filter((x) => f === "all" || (f === "disabled" ? !x.enabled : x.kind === f))
        .filter((x) => !q || [x.name, (farmById(x.farm_id) || {}).name, x.guacamole_connection_id, x.guacamole_connection_name, x.remote_app].join(" ").toLowerCase().includes(q))
        .sort((a, b) => a.name.localeCompare(b.name, "de"));
      $("resCards").innerHTML = rows.length
        ? rows
            .map((x) => {
              const h = resourceReadiness(x),
                farm = farmById(x.farm_id);
              return (
                '<div class="card entity"' +
                (x.enabled ? "" : ' style="opacity:.7"') +
                '><div class="entity-head"><div><div class="entity-title">' +
                icon(x.kind === "remoteapp" ? "app" : "monitor") +
                " " +
                esc(x.name) +
                '</div><div class="small muted">' +
                esc(farm ? farm.name : x.farm_id) +
                '</div></div><div class="row" style="gap:4px">' +
                (x.enabled ? "" : SG.badge("Deaktiviert", "bad")) +
                '<span class="badge ' +
                h.cls +
                '" title="' +
                esc(h.detail) +
                '">' +
                esc(h.text) +
                '</span></div></div><div class="row" style="gap:4px">' +
                SG.badge(x.kind === "remoteapp" ? "RemoteApp" : "Desktop") +
                SG.badge("Guacamole " + (x.guacamole_connection_id ? "#" + x.guacamole_connection_id : x.guacamole_connection_name || "–")) +
                (x.remote_app ? SG.badge(x.remote_app) : "") +
                (x.manage_remote_app ? SG.badge("Agent-verwaltet", "info") : "") +
                (x.multi_monitor ? SG.badge((x.max_monitors || 2) + " Monitore", "ok", "monitor") : "") +
                "</div>" +
                (x.manage_remote_app ? '<div class="small mono muted ellipsis" title="' + esc(x.remote_app_path) + '">' + esc(x.remote_app_path || "Executable fehlt") + "</div>" : "") +
                '<div class="row end"><button class="btn sm" data-res-act="toggle" data-id="' +
                esc(x.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("power") +
                (x.enabled ? "Deaktivieren" : "Aktivieren") +
                '</button><button class="btn sm icon" title="Duplizieren" data-res-act="copy" data-id="' +
                esc(x.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("copy") +
                '</button><button class="btn sm" data-res-act="edit" data-id="' +
                esc(x.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("edit") +
                'Bearbeiten</button><button class="btn sm danger icon" title="Löschen" data-res-act="delete" data-id="' +
                esc(x.id) +
                '"' +
                noPerm("manage") +
                ">" +
                icon("trash") +
                "</button></div></div>"
              );
            })
            .join("")
        : '<div class="card">' + SG.empty(S.resources.length ? "Keine Resources für diesen Filter." : "Noch keine Resources. Lege einen Desktop oder eine RemoteApp an.", "app") + "</div>";
    },
    async act(act, id) {
      const x = S.resources.find((r) => r.id === id);
      if (act === "delete") {
        if (!(await SG.confirm({ title: "Resource löschen", message: "Resource „" + x.name + "“ löschen? Die Guacamole-Verbindung wird danach nicht mehr gebrokert.", confirm: "Löschen", danger: true }))) return;
        await api("/api/v1/resources/" + encodeURIComponent(id), { method: "DELETE" });
        SG.toast("Resource gelöscht");
        return tick(true);
      }
      if (act === "toggle") {
        await api("/api/v1/resources/" + encodeURIComponent(id), { method: "PUT", body: Object.assign({}, x, { enabled: !x.enabled }) });
        SG.toast(x.enabled ? "Resource deaktiviert" : "Resource aktiviert");
        return tick(true);
      }
      if (!S.farms.length) return SG.toast("Bitte zuerst eine Farm anlegen.", "warn");
      if (act === "copy") return resourceDialog(Object.assign(SG.clone(x), { id: "", name: x.name + " (Kopie)", enabled: false }), false);
      return resourceDialog(act === "edit" ? x : null, act === "edit");
    },
  };

  async function resourceDialog(x, editing) {
    x = x || { kind: "desktop", enabled: true, remote_app_command_line_setting: 0, max_monitors: 2 };
    const farmOpts = S.farms.map((f) => '<option value="' + esc(f.id) + '"' + (x.farm_id === f.id ? " selected" : "") + ">" + esc(f.name) + "</option>").join("");
    const fld = (id, label, val, extra) => '<label class="field"><span>' + esc(label) + '</span><input id="' + id + '" value="' + esc(val ?? "") + '"' + (extra || "") + "></label>";
    const body =
      '<div class="form"><div class="fields">' +
      fld("rName", "Name", x.name, ' placeholder="Sage 100"') +
      '<label class="field"><span>Typ</span><select id="rKind"><option value="desktop"' +
      (x.kind !== "remoteapp" ? " selected" : "") +
      '>Desktop</option><option value="remoteapp"' +
      (x.kind === "remoteapp" ? " selected" : "") +
      '>RemoteApp</option></select></label><label class="field"><span>Farm</span><select id="rFarm">' +
      farmOpts +
      "</select></label>" +
      fld("rConnID", "Guacamole Connection ID", x.guacamole_connection_id, ' placeholder="z. B. 12"') +
      fld("rConnName", "Guacamole Connection Name", x.guacamole_connection_name, ' placeholder="Sage"') +
      '</div><label class="switch"><input type="checkbox" id="rEnabled"' +
      (x.enabled ? " checked" : "") +
      '> Resource aktiv</label><div data-remote class="stack"><div class="fields">' +
      fld("rApp", "RemoteApp Alias", x.remote_app, ' placeholder="||Sage"') +
      fld("rDir", "Guacamole Working Dir", x.remote_app_dir) +
      fld("rArgs", "Guacamole Argumente", x.remote_app_args) +
      '</div><div class="note"><strong>Agent-gesteuerte RemoteApp-Veröffentlichung</strong><br>SessionGuard hält den Alias auf allen Farm-Servern im Sollzustand; Server, auf denen die App nicht bereit ist, werden für diese Resource nicht gebrokert.</div><label class="switch"><input type="checkbox" id="rManage"' +
      (x.manage_remote_app ? " checked" : "") +
      '> RemoteApp auf Farm-Servern automatisch veröffentlichen und überwachen</label><div data-managed class="fields">' +
      fld("rPath", "Executable auf dem Terminalserver", x.remote_app_path, ' placeholder="C:\\Program Files\\Hersteller\\app.exe"') +
      fld("rIcon", "Icon-Pfad (optional)", x.remote_app_icon_path, ' placeholder="leer = Executable"') +
      fld("rIconIdx", "Icon-Index", x.remote_app_icon_index || 0, ' type="number"') +
      '<label class="field"><span>Command-Line-Policy</span><select id="rCmd">' +
      [
        ["0", "Keine Argumente erlauben"],
        ["1", "Argumente aus RDP/Guacamole erlauben"],
        ["2", "Vorgegebene Argumente erzwingen"],
      ]
        .map(([v, l]) => '<option value="' + v + '"' + (String(x.remote_app_command_line_setting || 0) === v ? " selected" : "") + ">" + l + "</option>")
        .join("") +
      "</select></label>" +
      fld("rRequired", "Vorgegebene Argumente", x.remote_app_required_command_line) +
      '<div class="field"><span>&nbsp;</span><label class="switch"><input type="checkbox" id="rPortal"' +
      (x.remote_app_show_in_portal ? " checked" : "") +
      '> Auch in RD Web Access anzeigen</label></div></div></div><details class="section"' +
      (x.multi_monitor ? " open" : "") +
      "><summary>" +
      icon("monitor") +
      'Multi-Monitor (Span)</summary><div class="section-body"><div class="note">Die Guacamole-Sitzung kann über mehrere lokale Monitore aufgezogen werden; die RDP-Auflösung folgt per <code>display-update</code>. Voraussetzung: Guacamole-Verbindung mit <code>resize-method = display-update</code> und Zuordnung über die Connection ID.</div><label class="switch"><input type="checkbox" id="rMM"' +
      (x.multi_monitor ? " checked" : "") +
      '> Multi-Monitor für diese Resource erlauben</label><div class="fields" data-mm><label class="field"><span>Maximale Monitore</span><select id="rMaxMon">' +
      [2, 3, 4].map((n) => '<option value="' + n + '"' + ((x.max_monitors || 2) === n ? " selected" : "") + ">" + n + " Monitore</option>").join("") +
      '</select></label><label class="field"><span>Nur für Gruppen (optional, kommagetrennt)</span><input id="rMMGroups" value="' +
      esc((x.multi_monitor_groups || []).join(", ")) +
      '" placeholder="rds-multimonitor, it-admins"></label></div></div></details></div>';
    const sync = () => {
      const remote = $("rKind").value === "remoteapp";
      qs("[data-remote]").hidden = !remote;
      if (!remote) $("rManage").checked = false;
      qs("[data-managed]").hidden = !(remote && $("rManage").checked);
      qs("[data-mm]").hidden = !$("rMM").checked;
    };
    await SG.modal({
      title: editing ? "Resource bearbeiten" : "Resource anlegen",
      subtitle: editing ? "ID " + x.id : "Desktop oder RemoteApp für eine Farm veröffentlichen",
      size: "wide",
      body,
      actions: [
        { label: "Abbrechen", value: null },
        { label: editing ? "Änderungen speichern" : "Resource anlegen", kind: "primary", value: "save" },
      ],
      onOpen: (dlg) => {
        sync();
        dlg.addEventListener("change", sync);
      },
      onAction: async () => {
        const name = $("rName").value.trim(),
          farm_id = $("rFarm").value;
        if (!name || !farm_id) {
          SG.toast("Name und Farm sind erforderlich", "warn");
          return false;
        }
        const mm = $("rMM").checked;
        const body = {
          name,
          kind: $("rKind").value,
          farm_id,
          guacamole_connection_id: $("rConnID").value.trim(),
          guacamole_connection_name: $("rConnName").value.trim(),
          remote_app: $("rApp").value.trim(),
          remote_app_dir: $("rDir").value.trim(),
          remote_app_args: $("rArgs").value.trim(),
          manage_remote_app: $("rManage").checked,
          remote_app_path: $("rPath").value.trim(),
          remote_app_icon_path: $("rIcon").value.trim(),
          remote_app_icon_index: +$("rIconIdx").value,
          remote_app_command_line_setting: +$("rCmd").value,
          remote_app_required_command_line: $("rRequired").value,
          remote_app_show_in_portal: $("rPortal").checked,
          multi_monitor: mm,
          max_monitors: mm ? +$("rMaxMon").value : 0,
          multi_monitor_groups: mm
            ? $("rMMGroups")
                .value.split(/[,;]/)
                .map((s) => s.trim())
                .filter(Boolean)
            : [],
          enabled: $("rEnabled").checked,
        };
        await api(editing ? "/api/v1/resources/" + encodeURIComponent(x.id) : "/api/v1/resources", { method: editing ? "PUT" : "POST", body });
        SG.toast(editing ? "Resource aktualisiert" : "Resource angelegt");
        await tick(true);
      },
    });
  }

  // ------------------------------------------------------------- access --
  views.access = {
    title: "Zugriff & Identitäten",
    interval: REFRESH_MS,
    async load() {
      if (!can("manage")) return;
      const [as, ib, ad] = await Promise.all([
        api("/api/v1/access/sessions").catch((e) => ({ error: e.message })),
        api("/api/v1/access/identities").catch((e) => ({ error: e.message })),
        api("/api/v1/admin/sessions").catch((e) => ({ error: e.message })),
      ]);
      S.access = as;
      S.identities = ib;
      S.adminSessions = ad;
    },
    mount() {
      ["access", "identities", "admins"].forEach((t) => SG.onTable(t, () => this.update()));
      $("view").innerHTML =
        pageHead("Zugriff & Identitäten", "Guacamole Access-Sessions (ForwardAuth), Identitätsbindungen Benutzername → OIDC-Subject und Anmeldungen an dieser Konsole.") +
        (can("manage")
          ? '<div class="stack"><div class="card"><div class="card-head"><div><h3>Guacamole Access-Sessions</h3><div class="hint">Serverseitige Sitzungen, die der Reverse Proxy vor jedem Guacamole-Request prüft. Widerrufene Sessions liefern sofort keinen Benutzer-Header mehr.</div></div></div><div class="toolbar">' +
            SG.searchBox("access", "Benutzer, E-Mail, Gruppe…") +
            '</div><div id="accTable"></div></div><div class="card"><div class="card-head"><div><h3>Identitätsbindungen</h3><div class="hint">Jeder Guacamole-Benutzername ist fest an die unveränderliche OIDC-Subject-ID gebunden – eine Umbenennung im IdP kann kein fremdes Konto übernehmen.</div></div><button class="btn sm danger" data-acc="reset">' +
            icon("alert") +
            'Alle zurücksetzen (IdP-Wechsel)</button></div><div class="toolbar">' +
            SG.searchBox("identities", "Benutzername, Subject, Issuer…") +
            '</div><div id="idTable"></div></div><div class="card"><div class="card-head"><div><h3>Admin-Sessions (Director)</h3><div class="hint">Anmeldungen an dieser Konsole. Ein Widerruf wirkt sofort.</div></div></div><div id="admTable"></div></div></div>'
          : '<div class="card">' + SG.empty("Diese Ansicht erfordert die Berechtigung „manage“ (Admin).", "shield") + "</div>");
      $("view").addEventListener("click", (e) => {
        const b = e.target.closest("[data-acc]");
        if (b) this.act(b.dataset.acc, b.dataset.id, b.dataset.user).catch(SG.fail);
      });
    },
    update() {
      if (!can("manage")) return;
      const err = (x) => (x && x.error ? SG.empty(x.error, "alert") : null);
      $("accTable").innerHTML =
        err(S.access) ||
        SG.table({
          id: "access",
          rows: (S.access || {}).sessions || [],
          empty: "Keine aktiven Guacamole Access-Sessions.",
          emptyIcon: "shield",
          search: (x) => [x.username, x.name, x.email, (x.groups || []).join(" "), (x.guacamole_groups || []).join(" ")].join(" "),
          columns: [
            { key: "username", label: "Benutzer", html: (x) => "<strong>" + esc(x.username || "–") + "</strong>" + (x.name || x.email ? '<div class="small muted">' + esc([x.name, x.email].filter(Boolean).join(" · ")) + "</div>" : "") },
            { key: "groups", label: "OIDC-Gruppen", html: (x) => (x.groups || []).map((g) => SG.badge(g)).join(" ") || "–", value: (x) => (x.groups || []).join(",") },
            {
              key: "guac_groups",
              label: "Guacamole-Gruppen",
              html: (x) => (x.guacamole_groups || []).map((g) => SG.badge(g, "info")).join(" ") || '<span class="small muted">keine</span>',
              value: (x) => (x.guacamole_groups || []).join(","),
            },
            { key: "created_at", label: "Angemeldet", html: (x) => SG.time(x.created_at) },
            { key: "expires_at", label: "Läuft ab", html: (x) => SG.time(x.expires_at) },
            { key: "act", label: "", cls: "actions", sort: false, html: (x) => '<button class="btn sm danger" data-acc="revoke-access" data-id="' + esc(x.id) + '" data-user="' + esc(x.username || "") + '">' + icon("x") + "Widerrufen</button>" },
          ],
        });
      $("idTable").innerHTML =
        err(S.identities) ||
        SG.table({
          id: "identities",
          rows: (S.identities || {}).bindings || [],
          empty: "Noch keine Identitätsbindungen.",
          emptyIcon: "key",
          search: (x) => [x.username, x.subject, x.issuer].join(" "),
          columns: [
            { key: "username", label: "Benutzername", html: (x) => "<strong>" + esc(x.username) + "</strong>" },
            { key: "subject", label: "OIDC Subject", html: (x) => "<code>" + esc(x.subject) + "</code>" + (x.issuer ? '<div class="small muted">' + esc(x.issuer) + "</div>" : "") },
            { key: "created_at", label: "Gebunden seit", html: (x) => SG.time(x.created_at) },
            { key: "last_seen", label: "Zuletzt", html: (x) => SG.time(x.last_seen) },
            { key: "act", label: "", cls: "actions", sort: false, html: (x) => '<button class="btn sm" data-acc="release" data-user="' + esc(x.username) + '">' + icon("undo") + "Freigeben</button>" },
          ],
        });
      $("admTable").innerHTML =
        err(S.adminSessions) ||
        SG.table({
          id: "admins",
          rows: (S.adminSessions || {}).sessions || [],
          empty: "Keine aktiven Admin-Sessions.",
          columns: [
            { key: "user", label: "Benutzer", html: (x) => "<strong>" + esc(x.email || x.name || x.subject) + "</strong>" + (x.name && x.email ? '<div class="small muted">' + esc(x.name) + "</div>" : ""), value: (x) => x.email || x.name || x.subject },
            { key: "groups", label: "Gruppen", html: (x) => (x.groups || []).map((g) => SG.badge(g)).join(" ") || "–", value: (x) => (x.groups || []).join(",") },
            { key: "created_at", label: "Angemeldet", html: (x) => SG.time(x.created_at) },
            { key: "last_seen", label: "Zuletzt aktiv", html: (x) => SG.time(x.last_seen) },
            { key: "expires_at", label: "Läuft ab", html: (x) => SG.time(x.expires_at) },
            { key: "act", label: "", cls: "actions", sort: false, html: (x) => '<button class="btn sm danger" data-acc="revoke-admin" data-id="' + esc(x.id) + '" data-user="' + esc(x.email || x.subject) + '">' + icon("x") + "Widerrufen</button>" },
          ],
        });
    },
    async act(act, id, user) {
      if (act === "revoke-access") {
        if (!(await SG.confirm({ title: "Access-Session widerrufen", message: "Access-Session von " + (user || "diesem Benutzer") + " widerrufen? Guacamole-Tunnel werden innerhalb von ca. 30 s geschlossen.", confirm: "Widerrufen", danger: true }))) return;
        await api("/api/v1/access/sessions/" + encodeURIComponent(id), { method: "DELETE" });
        SG.toast("Access-Session widerrufen");
      } else if (act === "revoke-admin") {
        if (!(await SG.confirm({ title: "Admin-Session widerrufen", message: "Admin-Session von " + (user || "diesem Benutzer") + " widerrufen?", confirm: "Widerrufen", danger: true }))) return;
        await api("/api/v1/admin/sessions/" + encodeURIComponent(id), { method: "DELETE" });
        SG.toast("Admin-Session widerrufen");
      } else if (act === "release") {
        if (!(await SG.confirm({ title: "Identitätsbindung freigeben", message: "Bindung für „" + user + "“ freigeben? Alle Access-Sessions dieses Benutzers werden beendet; die nächste Anmeldung bindet den Namen neu. Nur nach einer legitimen Umbenennung im IdP verwenden.", confirm: "Freigeben", danger: true }))) return;
        await api("/api/v1/access/identities/" + encodeURIComponent(user), { method: "DELETE" });
        SG.toast("Identitätsbindung freigegeben");
      } else if (act === "reset") {
        if (!(await SG.confirm({ title: "Alle Identitätsbindungen zurücksetzen", message: "Löscht ALLE Identitätsbindungen und beendet alle Access-Sessions. Nur bei einem IdP-Wechsel (z. B. PocketID → Keycloak) sinnvoll.", confirm: "Alles zurücksetzen", danger: true, typeToConfirm: "RESET" }))) return;
        const r = await api("/api/v1/access/identities?confirm=all", { method: "DELETE" });
        SG.toast((r.released || 0) + " Bindungen zurückgesetzt");
      }
      await tick(true);
    },
  };

  // ----------------------------------------------------------- policies --
  views.policies = {
    title: "Policies",
    interval: SLOW_MS,
    async load() {
      const [g, fr] = await Promise.all([api("/api/v1/policy/global"), api("/api/v1/farms")]);
      S.globalPolicy = g.policy || null;
      S.farms = fr.farms || [];
      if (can("policy")) {
        S.policyHistory = (await api("/api/v1/policy/history").catch(() => ({ history: [] }))).history || [];
        S.loadedAt.ph = Date.now();
      }
    },
    mount() {
      SG.onTable("polHist", () => this.update());
      $("view").innerHTML =
        pageHead("Policies", "Vererbung: Server-Override → Farm-Policy → globale Policy. Ohne Master-Policy nutzt ein Agent seine lokale Policy aus agent.json.") +
        '<div class="grid grid-3"><div class="card"><div class="card-head"><h3>Globale Policy</h3><button class="btn sm primary" data-pol="edit-global"' +
        noPerm("policy") +
        ">" +
        icon("edit") +
        'Bearbeiten</button></div><div class="card-body" id="polGlobal"></div></div><div class="card"><div class="card-head"><h3>Farm-Policies</h3></div><div class="card-body flush" id="polFarms"></div></div><div class="card"><div class="card-head"><h3>Server-Overrides</h3></div><div class="card-body flush" id="polOverrides"></div></div></div><div class="card" style="margin-top:16px"><div class="card-head"><div><h3>Policy-Historie &amp; Rollback</h3><div class="hint">Jede Änderung erzeugt eine neue Revision.</div></div></div><div class="toolbar">' +
        SG.searchBox("polHist", "Ziel, Revision, Akteur…") +
        '</div><div id="polHistTable"></div></div>';
      $("view").addEventListener("click", (e) => {
        const b = e.target.closest("[data-pol],[data-act]");
        if (!b || b.disabled) return;
        if (b.dataset.pol === "edit-global") globalPolicyDialog().catch(SG.fail);
        else if (b.dataset.pol === "farm") farmPolicyDialog(farmById(b.dataset.id)).catch(SG.fail);
        else if (b.dataset.act === "rollback") rollbackPolicy(b.dataset.target, b.dataset.revision).catch(SG.fail);
        else if (b.dataset.act === "view-rev") viewRevision(b.dataset.target, b.dataset.revision);
      });
    },
    update() {
      const g = S.globalPolicy;
      $("polGlobal").innerHTML = g
        ? '<div class="kv"><div><div class="k">Revision</div><div class="v"><code>' +
          esc(g.revision) +
          '</code></div></div><div><div class="k">Geändert</div><div class="v">' +
          SG.time(g.updated_at) +
          '</div></div></div><div class="row" style="margin-top:12px;gap:4px">' +
          SG.badge("Profil-Sync " + ((g.profiles || {}).enabled ? "an" : "aus"), (g.profiles || {}).enabled ? "ok" : "") +
          SG.badge("Steuerung " + ((g.sessions || {}).control_enabled ? "an" : "aus"), (g.sessions || {}).control_enabled ? "ok" : "") +
          SG.badge("Auto-Logoff " + ((g.sessions || {}).disconnected_logoff_enabled ? "an" : "aus"), (g.sessions || {}).disconnected_logoff_enabled ? "warn" : "") +
          SG.badge("Cleanup " + ((g.cleanup || {}).enabled ? ((g.cleanup || {}).dry_run ? "Dry-Run" : "live") : "aus"), (g.cleanup || {}).enabled ? "warn" : "") +
          SG.badge((g.templates || []).length + " Templates") +
          "</div>"
        : SG.empty("Keine globale Policy gesetzt.", "file");
      const fp = S.farms.filter((f) => f.policy);
      $("polFarms").innerHTML = fp.length
        ? '<div class="list" style="padding:12px">' +
          fp.map((f) => '<div class="row between"><span><strong>' + esc(f.name) + '</strong> <span class="small muted">' + SG.time(f.policy.updated_at) + '</span></span><button class="btn sm" data-pol="farm" data-id="' + esc(f.id) + '">' + icon("edit") + "Öffnen</button></div>").join("") +
          "</div>"
        : SG.empty("Keine Farm hat eine eigene Policy.", "layers");
      const ov = S.agents.filter((a) => a.desired_policy && a.desired_policy.revision);
      $("polOverrides").innerHTML = ov.length
        ? '<div class="list" style="padding:12px">' + ov.map((a) => '<div class="row between"><span>' + link("servers/" + encodeURIComponent(a.id) + "/policy", "<strong>" + esc(a.name) + "</strong>") + '</span><span class="small muted">' + SG.time(a.desired_policy.updated_at) + "</span></div>").join("") + "</div>"
        : SG.empty("Kein Server hat einen eigenen Override.", "server");
      $("polHistTable").innerHTML = can("policy") ? policyHistoryTable("polHist", (S.policyHistory || []).slice().reverse(), true) : SG.empty("Policy-Historie erfordert die Rolle Policy-Admin oder Admin.", "shield");
    },
  };

  async function globalPolicyDialog() {
    let ed;
    const g = (await api("/api/v1/policy/global")).policy;
    await SG.modal({
      title: "Globale Policy",
      subtitle: g ? "Revision " + g.revision : "Noch keine globale Policy – Vorlage: Standardwerte",
      size: "xwide",
      body: '<div class="note warn" style="margin-bottom:14px">Gilt für alle Server ohne eigenen Override und ohne Farm-Policy.</div><div id="globPolEd"></div>',
      actions: [
        { label: "Abbrechen", value: null },
        { label: "Globale Policy speichern", kind: "primary", value: "save" },
      ],
      onOpen: () => (ed = SG.PolicyEditor($("globPolEd"), g || activePolicy({}))),
      guard: () => !ed || !ed.isDirty() || confirm("Ungespeicherte Änderungen verwerfen?"),
      onAction: async () => {
        await api("/api/v1/policy/all", { method: "PUT", body: ed.collect() });
        SG.toast("Globale Policy gespeichert");
        S.policyHistory = null;
        await tick(true);
      },
    });
  }

  // ------------------------------------------------------------- alerts --
  views.alerts = {
    title: "Alerts",
    interval: REFRESH_MS,
    async load() {
      S.alerts = (await api("/api/v1/alerts")).alerts || [];
    },
    mount() {
      SG.onTable("alerts", () => this.update());
      $("view").innerHTML =
        pageHead("Alerts", "Offline-Agents, CPU/RAM/Disk, Health, Profilfehler, getrennte Sitzungen und langsame Logons.") +
        '<div class="card"><div class="toolbar">' +
        SG.searchBox("alerts", "Server, Typ, Meldung…") +
        '<div class="btn-group" id="alFilter"><button class="btn sm" data-f="active">Aktiv</button><button class="btn sm" data-f="all">Alle</button></div></div><div id="alTable"></div></div>';
      $("alFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (b) {
          S.filters.alerts = b.dataset.f;
          this.update();
        }
      };
    },
    update() {
      qsa("#alFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.alerts));
      $("alTable").innerHTML = SG.table({
        id: "alerts",
        rows: S.alerts.filter((x) => S.filters.alerts === "all" || x.active),
        empty: S.filters.alerts === "active" ? "Keine aktiven Alerts." : "Keine Alerts.",
        emptyIcon: "check",
        defaultSort: "last_seen_at",
        defaultDir: -1,
        search: (x) => [x.hostname, x.type, x.message, x.severity].join(" "),
        columns: [
          { key: "active", label: "Status", html: (x) => (x.active ? SG.badge("Aktiv", "bad", "dot") : SG.badge("Gelöst", "ok")), value: (x) => (x.active ? 0 : 1) },
          { key: "severity", label: "Schwere", html: (x) => SG.badge(x.severity || "–", x.severity === "critical" ? "bad" : "warn") },
          { key: "hostname", label: "Server", html: (x) => (x.agent_id ? link("servers/" + encodeURIComponent(x.agent_id), esc(x.hostname || x.agent_id)) : esc(x.hostname || "–")) },
          { key: "type", label: "Typ", html: (x) => "<strong>" + esc(x.type) + "</strong>" },
          { key: "message", label: "Meldung", html: (x) => esc(x.message) },
          { key: "first_seen_at", label: "Seit", html: (x) => SG.time(x.first_seen_at) },
          { key: "last_seen_at", label: "Zuletzt", html: (x) => (x.active ? SG.time(x.last_seen_at) : "gelöst " + SG.time(x.resolved_at)) },
        ],
      });
    },
  };

  // ------------------------------------------------------------ history --
  views.history = {
    title: "Session-Historie",
    interval: SLOW_MS,
    async load() {
      const q = new URLSearchParams({ limit: S.hist.limit });
      if (S.hist.user) q.set("user", S.hist.user);
      if (S.hist.agent) q.set("agent", S.hist.agent);
      S.history = (await api("/api/v1/history?" + q)).history || [];
    },
    mount() {
      SG.onTable("history", () => this.update());
      $("view").innerHTML =
        pageHead("Session-Historie", "Logon, Reconnect, Disconnect, Logoff und SessionGuard-Ereignisse aller Server.") +
        '<div class="card"><div class="toolbar"><label class="field" style="min-width:180px"><span>Benutzer (Server-Filter)</span><input class="input" id="hUser" value="' +
        esc(S.hist.user) +
        '" placeholder="DOMAIN\\user"></label><label class="field"><span>Server</span><select class="input" id="hAgent"><option value="">Alle Server</option>' +
        S.agents.map((a) => '<option value="' + esc(a.id) + '"' + (S.hist.agent === a.id ? " selected" : "") + ">" + esc(a.name) + "</option>").join("") +
        '</select></label><label class="field"><span>Anzahl</span><select class="input" id="hLimit">' +
        [200, 500, 1000, 5000].map((n) => '<option' + (S.hist.limit === n ? " selected" : "") + ">" + n + "</option>").join("") +
        '</select></label><button class="btn primary" id="hApply" style="align-self:flex-end">' +
        icon("search") +
        'Laden</button><div class="grow"></div><div style="align-self:flex-end" class="row">' +
        SG.searchBox("history", "In Ergebnissen suchen…") +
        '<button class="btn" id="hCsv">' +
        icon("download") +
        'CSV</button></div></div><div id="hTable" style="--table-max:70vh"></div></div>';
      const apply = async () => {
        S.hist = { user: $("hUser").value.trim(), agent: $("hAgent").value, limit: +$("hLimit").value };
        await this.load().catch(SG.fail);
        this.update();
      };
      $("hApply").onclick = apply;
      $("hUser").onkeydown = (e) => e.key === "Enter" && apply();
      $("hCsv").onclick = () =>
        SG.csv("sessionguard-historie.csv", [
          { label: "Zeit", csv: (r) => r.time },
          { label: "Server", csv: (r) => r.hostname },
          { label: "Benutzer", csv: (r) => r.user },
          { label: "Ereignis", csv: (r) => r.event },
          { label: "Sitzung", csv: (r) => r.session_id },
          { label: "Status", csv: (r) => r.state },
          { label: "Client", csv: (r) => r.client_name },
          { label: "Details", csv: (r) => r.details },
        ], S.history.slice().reverse());
    },
    update() {
      $("hTable").innerHTML = SG.table({
        id: "history",
        rows: S.history.slice().reverse(),
        empty: "Keine Session-Historie für diese Filter.",
        emptyIcon: "clock",
        search: (r) => [r.hostname, r.user, r.event, r.client_name, r.details].join(" "),
        columns: [
          { key: "time", label: "Zeit", html: (r) => '<span title="' + esc(SG.when(r.time)) + '">' + esc(SG.when(r.time)) + "</span>" },
          { key: "hostname", label: "Server", html: (r) => (r.agent_id ? link("servers/" + encodeURIComponent(r.agent_id), esc(r.hostname)) : esc(r.hostname)) },
          { key: "user", label: "Benutzer", html: (r) => "<strong>" + esc(r.user || "–") + "</strong>" },
          { key: "event", label: "Ereignis", html: (r) => SG.badge(r.event, eventKind(r.event)) },
          { key: "session_id", label: "Sitzung", cls: "num" },
          { key: "client_name", label: "Client", html: (r) => esc(r.client_name || "–") },
          { key: "details", label: "Details", html: (r) => '<span class="small">' + esc(r.details || "–") + "</span>" },
        ],
      });
    },
  };

  // -------------------------------------------------------------- audit --
  views.audit = {
    title: "Audit-Log",
    interval: SLOW_MS,
    async load() {
      if (!can("audit")) return;
      S.audit = (await api("/api/v1/audit")).audit || [];
    },
    mount() {
      SG.onTable("audit", () => this.update());
      $("view").innerHTML =
        pageHead("Audit-Log", "Alle administrativen Aktionen, Agent-Ergebnisse und Sicherheitsereignisse.") +
        (can("audit")
          ? '<div class="card"><div class="toolbar">' +
            SG.searchBox("audit", "Akteur, Aktion, Ziel, Details…") +
            '<div class="btn-group" id="auFilter"><button class="btn sm" data-f="all">Alle</button><button class="btn sm" data-f="error">Fehler</button><button class="btn sm" data-f="denied">Abgelehnt</button></div><div class="grow"></div><button class="btn sm" id="auCsv">' +
            icon("download") +
            'CSV</button></div><div id="auTable" style="--table-max:72vh"></div></div>'
          : '<div class="card">' + SG.empty("Das Audit-Log erfordert die Rolle Auditor oder Admin.", "shield") + "</div>");
      if (!can("audit")) return;
      $("auFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (b) {
          S.filters.audit = b.dataset.f;
          this.update();
        }
      };
      $("auCsv").onclick = () =>
        SG.csv("sessionguard-audit.csv", [
          { label: "Zeit", csv: (r) => r.time },
          { label: "Akteur", csv: (r) => r.actor },
          { label: "Aktion", csv: (r) => r.action },
          { label: "Ziel", csv: (r) => r.target },
          { label: "Ergebnis", csv: (r) => r.result },
          { label: "Details", csv: (r) => r.details },
        ], this.rows());
    },
    rows() {
      const f = S.filters.audit;
      return S.audit
        .slice()
        .reverse()
        .filter((x) => f === "all" || x.result === f || (f === "denied" && x.result === "warning"));
    },
    update() {
      if (!can("audit")) return;
      qsa("#auFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.audit));
      $("auTable").innerHTML = SG.table({
        id: "audit",
        rows: this.rows(),
        max: 1000,
        empty: "Noch keine Audit-Einträge.",
        search: (x) => [x.actor, x.action, x.target, x.result, x.details].join(" "),
        columns: [
          { key: "time", label: "Zeit", html: (x) => esc(SG.when(x.time)) },
          { key: "actor", label: "Akteur", html: (x) => esc(x.actor) },
          { key: "action", label: "Aktion", html: (x) => "<code>" + esc(x.action) + "</code>" },
          { key: "target", label: "Ziel", html: (x) => esc(x.target || "–") },
          { key: "result", label: "Ergebnis", html: (x) => SG.badge(x.result, x.result === "error" || x.result === "denied" ? "bad" : x.result === "success" ? "ok" : x.result === "warning" ? "warn" : "info") },
          { key: "details", label: "Details", html: (x) => '<span class="small">' + esc(x.details || "") + "</span>" },
        ],
      });
    },
  };

  // =========================================================== ROUTER ====
  function parseRoute() {
    const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(decodeURIComponent);
    let name = parts[0] || "dashboard";
    if (name === "servers" && parts[1]) return { name: "server", params: parts.slice(1) };
    if (!views[name]) name = "dashboard";
    return { name, params: parts.slice(1) };
  }
  const view = () => views[S.route.name];

  async function navigate() {
    const next = parseRoute();
    const leavingDetail = S.route.name === "server" && (next.name !== "server" || next.params[0] !== S.route.params[0]);
    if (leavingDetail && S.detail && S.detail.rec && (SG.isDirty("policy:" + S.detail.rec.id) || SG.isDirty("control:" + S.detail.rec.id))) {
      if (!(await SG.confirm({ title: "Ungespeicherte Änderungen", message: "Policy oder Serversteuerung enthält ungespeicherte Änderungen. Seite trotzdem verlassen?", confirm: "Verwerfen", danger: true }))) {
        history.replaceState(null, "", S.lastHash || "#/dashboard");
        return;
      }
    }
    if (leavingDetail && views.server.leave) views.server.leave();
    if (next.name === "server" && S.route.name === "server" && next.params[0] === S.route.params[0] && S.detail) {
      // Tab change within the same server: no remount.
      S.route = next;
      S.lastHash = location.hash;
      if (next.params[1]) {
        S.detail.tab = next.params[1];
        views.server.showTab();
      }
      return;
    }
    S.route = next;
    S.lastHash = location.hash;
    const v = view();
    const old = $("view"),
      fresh = old.cloneNode(false);
    old.replaceWith(fresh);
    setCrumbs([{ label: v.title }]);
    v.mount();
    SG.hydrateIcons($("view"));
    renderChrome();
    if (v.update) v.update();
    await tick(true);
    window.scrollTo(0, 0);
  }

  // ============================================================= LOOP ====
  async function loadDashboard() {
    S.dash = await api("/api/v1/dashboard");
    S.agents = S.dash.agents || [];
    recordMetrics();
  }
  async function loadFarmsIfNeeded() {
    // Farms are needed for names on several views; refresh at most every 30 s.
    if (!S.loadedAt.farms || Date.now() - S.loadedAt.farms > SLOW_MS) {
      S.farms = (await api("/api/v1/farms")).farms || [];
      S.loadedAt.farms = Date.now();
    }
  }

  async function tick(force) {
    if (S.loading) return;
    if (!force && (S.paused || document.hidden)) return;
    S.loading = true;
    try {
      await loadDashboard();
      await loadFarmsIfNeeded();
      const v = view();
      const key = "view:" + S.route.name;
      if (v.load && (force || !S.loadedAt[key] || Date.now() - S.loadedAt[key] >= (v.interval || REFRESH_MS) - 200)) {
        await v.load();
        S.loadedAt[key] = Date.now();
      }
      S.error = null;
      S.lastOK = new Date();
      if (v.update) v.update();
    } catch (e) {
      S.error = e;
    } finally {
      S.loading = false;
      renderChrome();
    }
  }

  // Row links: <tr data-href="#/...">
  document.addEventListener("click", (e) => {
    const tr = e.target.closest("tr[data-href]");
    if (tr && !e.target.closest("a,button,input,label")) location.hash = tr.dataset.href;
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && !e.target.closest("input,textarea,select") && !document.querySelector("dialog[open]")) {
      const s = qs("[data-table-search]");
      if (s) {
        e.preventDefault();
        s.focus();
      }
    }
  });
  document.addEventListener("visibilitychange", () => !document.hidden && tick(false));

  async function init() {
    SG.hydrateIcons(document);
    SG.initTheme();
    SG.initNav();
    $("pauseBtn").onclick = () => {
      S.paused = !S.paused;
      renderChrome();
      if (!S.paused) tick(true);
    };
    $("refreshBtn").onclick = () => {
      S.loadedAt = {};
      tick(true);
    };
    try {
      S.me = await api("/api/v1/me");
      S.perms = new Set(S.me.permissions || []);
      renderUser();
    } catch (e) {
      S.error = e;
    }
    window.addEventListener("hashchange", navigate);
    await navigate();
    setInterval(() => tick(false), REFRESH_MS);
  }
  init();
})();
