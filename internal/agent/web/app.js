/*
 * SessionGuard local Agent console. Plain JavaScript (no build step) on top
 * of /ui/sg.js and /ui/policy-editor.js. Hash routes:
 *   #/overview  #/sessions  #/operations  #/policy
 *
 * All values are escaped with esc() before they are inserted as HTML.
 */
(function () {
  "use strict";
  const { $, qs, qsa, esc, api, icon } = SG;
  const REFRESH_MS = 5000;

  const S = {
    status: null,
    me: null,
    policy: null,
    editor: null,
    metrics: { cpu: [], ram: [], at: "" },
    paused: false,
    error: null,
    lastOK: null,
    loading: false,
    route: "overview",
    lastHash: "",
    filters: { sessions: "all", events: "all" },
  };
  const snap = () => (S.status && S.status.snapshot) || {};
  const userOf = (s) => (s.domain ? s.domain + "\\" : "") + (s.user || "");
  const memPct = (v) => (v && v.memory_total ? ((v.memory_total - v.memory_available) * 100) / v.memory_total : 0);
  const diskPct = (v) => (v && v.disk_total ? ((v.disk_total - v.disk_free) * 100) / v.disk_total : 0);
  const stateBadge = (st) => {
    const s = String(st || "").toLowerCase();
    return SG.badge(st || "–", s === "active" ? "ok" : s === "disconnected" ? "warn" : "");
  };
  const pageHead = (title, text, actions) =>
    '<div class="page-head"><div><h2>' + esc(title) + "</h2>" + (text ? "<p>" + esc(text) + "</p>" : "") + '</div><div class="row">' + (actions || "") + "</div></div>";

  function recordMetrics() {
    const s = snap(),
      v = s.server || {},
      m = S.metrics;
    const stamp = s.collected_at || (S.lastOK && S.lastOK.toISOString());
    if (stamp === m.at) return;
    m.at = stamp;
    m.cpu.push(Number(v.cpu_percent || 0));
    m.ram.push(memPct(v));
    if (m.cpu.length > 60) m.cpu.shift();
    if (m.ram.length > 60) m.ram.shift();
  }

  async function sessionAction(id, action, label) {
    const body = { action };
    if (action === "message") {
      const m = await SG.prompt({ title: "Nachricht senden", subtitle: label, label: "Nachricht", multiline: true, confirm: "Senden", icon: "message" });
      if (m === null) return;
      body.message = m;
      body.title = "SessionGuard";
    }
    if (action === "logoff" && !(await SG.confirm({ title: "Sitzung abmelden", message: label + " wirklich abmelden? Die Profil-Pipeline startet nach dem Sitzungsende.", confirm: "Abmelden", danger: true }))) return;
    if (action === "disconnect" && !(await SG.confirm({ title: "Sitzung trennen", message: label + " trennen? Die Sitzung bleibt erhalten.", confirm: "Trennen" }))) return;
    try {
      await api("/api/v1/sessions/" + id + "/action", { method: "POST", body });
      SG.toast("Sitzungsaktion ausgeführt");
      setTimeout(() => tick(true), 500);
    } catch (e) {
      SG.fail(e);
    }
  }

  // ------------------------------------------------------------- views --
  const views = {};

  views.overview = {
    title: "Übersicht",
    mount() {
      $("view").innerHTML =
        '<div id="ovHead"></div><div class="stats" id="ovStats"></div><div class="grid grid-3"><div class="card"><div class="card-head"><h3>Auslastung</h3><span class="small muted" id="ovSpan"></span></div><div class="card-body stack" id="ovLoad"></div></div><div class="card"><div class="card-head"><h3>Master-Verbindung</h3></div><div class="card-body" id="ovMaster"></div></div><div class="card"><div class="card-head"><h3>Health-Checks</h3></div><div class="card-body" id="ovChecks"></div></div></div><div class="card" style="margin-top:16px"><div class="card-head"><h3>Letzte Ereignisse</h3><a class="btn sm ghost" href="#/operations">Alle</a></div><div class="card-body flush" id="ovEvents"></div></div>';
    },
    update() {
      const s = snap(),
        v = s.server || {},
        h = s.health || {},
        ss = s.sessions || [];
      const apps = s.remote_apps || [];
      const stat = (href, ic, label, value, sub, tone) =>
        '<a class="stat' + (tone ? " tone-" + tone : "") + '" href="' + href + '"><span class="stat-label">' + icon(ic) + esc(label) + '</span><span class="stat-value">' + esc(value) + "</span>" + (sub ? '<span class="stat-sub">' + esc(sub) + "</span>" : "") + "</a>";
      $("ovHead").innerHTML = pageHead(v.hostname || "Lokaler Terminalserver", (v.os || "") + (v.version ? " " + v.version : "") + " · Agent " + (s.agent_version || "–") + (v.uptime_seconds ? " · Uptime " + SG.duration(v.uptime_seconds) : ""));
      $("ovStats").innerHTML =
        stat("#/sessions", "users", "Aktive Sitzungen", ss.filter((x) => x.state === "Active").length, ss.filter((x) => x.user).length + " Benutzer gesamt") +
        stat("#/sessions", "unplug", "Getrennt", ss.filter((x) => x.state === "Disconnected" && x.user).length) +
        stat("#/operations", "box", "Profil-Jobs", (s.profile_jobs || []).length) +
        stat("#/operations", "trash", "Cleanup ausstehend", (s.pending_cleanup || []).length) +
        stat("#/operations", "app", "RemoteApps bereit", apps.filter((x) => x.published && x.path_exists && x.in_sync).length + "/" + apps.length, "", apps.some((x) => !(x.published && x.path_exists && x.in_sync)) ? "warn" : "") +
        stat("#/overview", "activity", "Health", h.score ?? "–", "", (h.score || 0) < 50 ? "bad" : (h.score || 0) < 70 ? "warn" : "ok");
      const m = S.metrics;
      $("ovSpan").textContent = m.cpu.length > 1 ? "Verlauf seit " + Math.max(1, Math.round((m.cpu.length * REFRESH_MS) / 60000)) + " min" : "";
      $("ovLoad").innerHTML =
        SG.meter("CPU", v.cpu_percent, Number(v.cpu_percent || 0).toFixed(1) + " %") +
        SG.spark(m.cpu) +
        SG.meter("RAM", memPct(v), memPct(v).toFixed(1) + " % · " + SG.bytes(v.memory_available) + " frei") +
        SG.spark(m.ram, "ram") +
        (v.disk_total ? SG.meter("Systemdisk", diskPct(v), SG.bytes(v.disk_free) + " frei", [80, 92]) : '<div class="small muted">Systemdisk: ' + SG.bytes(v.disk_free) + " frei</div>");
      const st = S.status || {};
      $("ovMaster").innerHTML =
        '<div class="stack"><div class="row between">' +
        (st.master_url ? (st.master_error ? SG.badge("Gestört", "bad", "dot") : SG.badge("Verbunden", "ok", "dot")) : SG.badge("Kein Master konfiguriert")) +
        '<span class="small muted">Letzter Heartbeat ' +
        SG.time(st.last_master_ok) +
        "</span></div>" +
        (st.master_url ? '<div class="small"><span class="muted">Master:</span> <code>' + esc(st.master_url) + "</code></div>" : "") +
        (st.master_error ? '<div class="note bad">' + esc(st.master_error) + "</div>" : "") +
        '<div class="small muted">Policy-Revision: <code>' +
        esc(s.policy_revision || (s.policy || {}).revision || "–") +
        "</code></div></div>";
      const checks = (h.checks || []).length
        ? h.checks
        : [
            { name: "RDP-Listener", ok: h.rdp_listener_ok },
            { name: "Profil-Store", ok: h.profile_store_ok },
          ];
      $("ovChecks").innerHTML =
        '<div class="list">' +
        checks
          .map((c) => '<div class="row between"><span>' + icon(c.ok ? "check" : "alert", c.ok ? "good" : "bad") + " " + esc(c.name) + "</span>" + (c.message ? '<span class="small muted ellipsis" style="max-width:60%" title="' + esc(c.message) + '">' + esc(c.message) + "</span>" : SG.badge(c.ok ? "OK" : "Fehler", c.ok ? "ok" : "bad")) + "</div>")
          .join("") +
        "</div>";
      $("ovEvents").innerHTML = eventsTable("ovEv", (s.events || []).slice().reverse().slice(0, 8), false);
    },
  };

  views.sessions = {
    title: "Sitzungen",
    mount() {
      SG.onTable("sessions", () => this.update());
      $("view").innerHTML =
        pageHead("Benutzersitzungen", "Lokale RDS-Sitzungen dieses Servers. Aktionen erfordern die Sitzungssteuerung in der Policy.") +
        '<div id="sesBanner"></div><div class="card"><div class="toolbar">' +
        SG.searchBox("sessions", "Benutzer, Client…") +
        '<div class="btn-group" id="sesFilter">' +
        [
          ["all", "Alle"],
          ["active", "Aktiv"],
          ["disconnected", "Getrennt"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-f="' + k + '">' + l + "</button>")
          .join("") +
        '</div></div><div id="sesList"></div></div>';
      $("sesFilter").onclick = (e) => {
        const b = e.target.closest("[data-f]");
        if (b) {
          S.filters.sessions = b.dataset.f;
          this.update();
        }
      };
      $("view").addEventListener("click", (e) => {
        const b = e.target.closest("button[data-action]");
        if (b) sessionAction(+b.dataset.sid, b.dataset.action, b.dataset.label);
      });
    },
    update() {
      qsa("#sesFilter [data-f]").forEach((b) => b.classList.toggle("active", b.dataset.f === S.filters.sessions));
      const s = snap(),
        p = s.policy || {},
        control = !!(p.sessions && p.sessions.control_enabled);
      $("sesBanner").innerHTML = control ? "" : '<div class="banner warn">' + icon("alert") + "Sitzungssteuerung ist in der Policy deaktiviert.</div>";
      const f = S.filters.sessions;
      $("sesList").innerHTML = SG.table({
        id: "sessions",
        rows: (s.sessions || []).filter((x) => f === "all" || String(x.state).toLowerCase() === f),
        empty: "Keine Sitzungen.",
        emptyIcon: "users",
        search: (r) => [userOf(r), r.client_name, r.client_address, r.state, r.id].join(" "),
        columns: [
          { key: "id", label: "ID", cls: "num" },
          { key: "user", label: "Benutzer", html: (r) => (r.user ? "<strong>" + esc(userOf(r)) + "</strong>" : '<span class="muted">' + esc(r.station_name || "System") + "</span>"), value: userOf },
          { key: "state", label: "Status", html: (r) => stateBadge(r.state) },
          { key: "logon_at", label: "Logon", html: (r) => SG.time(r.logon_at), value: (r) => r.logon_at || "" },
          { key: "disc", label: "Getrennt seit", html: (r) => (r.disconnected_since ? esc(SG.when(r.disconnected_since)) + ' <span class="small muted">(' + esc(SG.since(r.disconnected_since)) + ")</span>" : "–"), value: (r) => r.disconnected_since || "" },
          { key: "client", label: "Client", html: (r) => esc(r.client_name || "–") + '<div class="small muted">' + esc(r.client_address || "") + "</div>", value: (r) => r.client_name || "" },
          {
            key: "act",
            label: "",
            cls: "actions",
            sort: false,
            html: (r) => {
              if (!control || !r.user) return "";
              const base = ' data-sid="' + r.id + '" data-label="' + esc(userOf(r) + " (Sitzung " + r.id + ")") + '"';
              return (
                '<button class="btn sm icon" title="Nachricht"' + base + ' data-action="message">' + icon("message") + '</button><button class="btn sm icon" title="Trennen"' + base + ' data-action="disconnect">' + icon("unplug") + '</button><button class="btn sm icon danger" title="Abmelden"' + base + ' data-action="logoff">' + icon("logout") + "</button>"
              );
            },
          },
        ],
      });
    },
  };

  function eventsTable(id, rows, searchable) {
    return SG.table({
      id,
      rows,
      empty: "Noch keine Ereignisse.",
      foot: searchable,
      search: searchable ? (r) => [r.message, r.user, r.level].join(" ") : null,
      rowAttrs: (r) => 'class="lvl-' + esc(r.level || "info") + '"',
      columns: [
        { key: "time", label: "Zeit", html: (r) => SG.time(r.time), value: (r) => r.time || "", sort: searchable },
        { key: "level", label: "Typ", html: (r) => SG.badge(r.level || "info", r.level === "error" ? "bad" : r.level === "warning" || r.level === "dry-run" ? "warn" : ""), sort: searchable },
        { key: "user", label: "Benutzer", html: (r) => esc(r.user || "–"), sort: searchable },
        { key: "message", label: "Meldung", html: (r) => esc(r.message || ""), sort: searchable },
      ],
    });
  }

  views.operations = {
    title: "Betriebsdaten",
    mount() {
      SG.onTable("events", () => this.update());
      $("view").innerHTML =
        pageHead("Betriebsdaten", "RemoteApp-Status, Profil-Pipeline und Agent-Ereignisse.") +
        '<div class="stack"><div class="card"><div class="card-head"><div><h3>RemoteApps</h3><div class="hint">Bei Agent-gesteuerten Apps erscheint der Status nach dem nächsten Master-Heartbeat.</div></div></div><div class="card-body flush" id="opApps"></div></div><div class="card"><div class="card-head"><h3>Profil-Pipeline</h3></div><div class="card-body flush" id="opProfiles"></div></div><div class="card"><div class="card-head"><h3>Ereignisse</h3><div class="btn-group" id="evFilter">' +
        [
          ["all", "Alle"],
          ["error", "Fehler"],
          ["warning", "Warnung"],
          ["info", "Info"],
        ]
          .map(([k, l]) => '<button class="btn sm" data-ev="' + k + '">' + l + "</button>")
          .join("") +
        '</div></div><div class="toolbar">' +
        SG.searchBox("events", "Meldung, Benutzer…") +
        '</div><div id="opEvents" style="--table-max:60vh"></div></div></div>';
      $("evFilter").onclick = (e) => {
        const b = e.target.closest("[data-ev]");
        if (b) {
          S.filters.events = b.dataset.ev;
          this.update();
        }
      };
    },
    update() {
      const s = snap();
      $("opApps").innerHTML = SG.table({
        id: "apps",
        rows: s.remote_apps || [],
        foot: false,
        empty: "Keine RemoteApps gefunden.",
        emptyIcon: "app",
        columns: [
          { key: "ok", label: "Status", html: (x) => (x.published && x.path_exists && x.in_sync ? SG.badge("Bereit", "ok") : SG.badge("Nicht bereit", "bad")), value: (x) => (x.published && x.path_exists && x.in_sync ? 0 : 1) },
          { key: "alias", label: "Alias", html: (x) => "<strong>||" + esc(x.alias) + '</strong><div class="small muted">' + esc(x.display_name || x.resource_id || "–") + "</div>" },
          { key: "path", label: "Pfad", html: (x) => '<span class="mono">' + esc(x.path || "–") + "</span>" },
          { key: "managed", label: "Verwaltung", html: (x) => (x.managed ? SG.badge("Master", "info") + ' <span class="small muted">' + (x.owned ? "angelegt" : "übernommen") + "</span>" : SG.badge("lokal entdeckt")) },
          { key: "error", label: "Fehler", html: (x) => (x.error ? '<span class="bad">' + esc(x.error) + "</span>" : "–") },
        ],
      });
      const jobs = s.profile_jobs || [],
        status = Object.values(s.profile_status || {});
      $("opProfiles").innerHTML =
        !jobs.length && !status.length
          ? SG.empty("Keine Profil-Jobs oder -Historie.", "box")
          : (jobs.length
              ? SG.table({
                  id: "jobs",
                  rows: jobs,
                  foot: false,
                  columns: [
                    { key: "operation", label: "Job", html: (j) => SG.badge(j.operation, "info") },
                    { key: "user", label: "Benutzer" },
                    { key: "due_at", label: "Fällig", html: (j) => SG.time(j.due_at) },
                    { key: "attempts", label: "Versuche", cls: "num", html: (j) => esc(j.attempts ?? 0) },
                    { key: "last_error", label: "Fehler", html: (j) => (j.last_error ? '<span class="bad">' + esc(j.last_error) + "</span>" : "–") },
                  ],
                })
              : "") +
            (status.length
              ? SG.table({
                  id: "pstatus",
                  rows: status,
                  foot: false,
                  columns: [
                    { key: "user", label: "Benutzer", html: (x) => esc(x.user || x.sid) },
                    { key: "last_backup_at", label: "Letztes Backup", html: (x) => SG.time(x.last_backup_at) },
                    { key: "last_restore_at", label: "Letzter Restore", html: (x) => SG.time(x.last_restore_at) },
                    { key: "err", label: "Status", html: (x) => (x.last_backup_error || x.last_restore_error ? '<span class="bad">' + esc(x.last_backup_error || x.last_restore_error) + "</span>" : SG.badge("OK", "ok")) },
                  ],
                })
              : "");
      const evf = S.filters.events;
      qsa("#evFilter [data-ev]").forEach((b) => b.classList.toggle("active", b.dataset.ev === evf));
      $("opEvents").innerHTML = eventsTable(
        "events",
        (s.events || [])
          .slice()
          .reverse()
          .filter((x) => evf === "all" || (x.level || "info") === evf)
          .slice(0, 300),
        true,
      );
    },
  };

  views.policy = {
    title: "Lokale Policy",
    async load(force) {
      if (S.editor && S.editor.isDirty() && !force) return;
      S.policy = await api("/api/v1/policy");
    },
    mount() {
      $("view").innerHTML =
        pageHead("Lokale Policy", "Fallback-Policy dieses Servers. Liefert der Master eine Policy (Server-Override, Farm oder global), überschreibt sie diese Einstellungen beim nächsten Heartbeat.") +
        '<div class="grid grid-3" id="polSummary" style="margin-bottom:16px"></div><div class="card"><div class="card-head"><div><h3>Policy bearbeiten</h3><div class="hint" id="polRev"></div></div><span class="dirty" id="polDirty" hidden>' +
        icon("edit") +
        'Ungespeichert</span></div><div class="card-body" id="polEditor">' +
        '<div class="skeleton" style="width:60%"></div></div><div class="card-foot"><button class="btn" id="polReload">' +
        icon("refresh") +
        'Neu laden</button><button class="btn primary" id="polSave">' +
        icon("check") +
        "Lokal speichern</button></div></div>";
      S.editor = null;
      $("polReload").onclick = async () => {
        if (S.editor && S.editor.isDirty() && !(await SG.confirm({ message: "Ungespeicherte Änderungen verwerfen und Policy neu laden?", confirm: "Verwerfen", danger: true }))) return;
        try {
          await this.load(true);
          this.renderEditor();
        } catch (e) {
          SG.fail(e);
        }
      };
      $("polSave").onclick = async () => {
        try {
          await api("/api/v1/policy", { method: "PUT", body: S.editor.collect() });
          SG.toast("Lokale Policy gespeichert");
          await this.load(true);
          this.renderEditor();
          tick(true);
        } catch (e) {
          SG.fail(e);
        }
      };
    },
    renderEditor() {
      S.editor = SG.PolicyEditor($("polEditor"), S.policy || {}, {
        onDirty: (v) => {
          $("polDirty").hidden = !v;
          SG.setDirty("agent-policy", v);
        },
      });
      SG.setDirty("agent-policy", false);
      $("polDirty").hidden = true;
    },
    update() {
      const p = S.policy;
      if (!p) return;
      if (!S.editor) this.renderEditor();
      $("polRev").textContent = "Revision " + (p.revision || "–") + (p.updated_at ? " · geändert " + SG.when(p.updated_at) : "");
      const pr = p.profiles || {},
        sp = p.sessions || {},
        c = p.cleanup || {};
      const card = (title, ic, badges, text) =>
        '<div class="card entity"><div class="entity-title">' + icon(ic) + " " + esc(title) + '</div><div class="row" style="gap:4px">' + badges + '</div><div class="small muted">' + esc(text) + "</div></div>";
      $("polSummary").innerHTML =
        card("Profil-Sync", "box", SG.badge(pr.enabled ? "Aktiv" : "Aus", pr.enabled ? "ok" : "") + SG.badge((pr.folders || []).length + " Ordner"), pr.store_root || "Kein Store konfiguriert") +
        card("Sitzungsrichtlinie", "users", SG.badge("Steuerung " + (sp.control_enabled ? "an" : "aus"), sp.control_enabled ? "ok" : "") + SG.badge("Auto-Logoff " + (sp.disconnected_logoff_enabled ? "an" : "aus"), sp.disconnected_logoff_enabled ? "warn" : ""), "Timeout: " + Math.round((sp.disconnected_timeout_seconds || 0) / 60) + " min") +
        card("Cleanup", "trash", SG.badge(c.enabled ? "Aktiv" : "Aus", c.enabled ? "ok" : "") + SG.badge(c.dry_run ? "Dry-Run" : "Live", c.dry_run ? "warn" : ""), "Grace: " + (c.grace_seconds || 0) + " s");
    },
  };

  // ------------------------------------------------------------ chrome --
  function renderChrome() {
    const live = $("live");
    live.classList.toggle("paused", S.paused);
    live.classList.toggle("error", !!S.error && !S.paused);
    $("liveText").textContent = S.paused ? "Pausiert" : S.error ? "Verbindung gestört" : S.lastOK ? "Live · " + S.lastOK.toLocaleTimeString("de-DE") : "Verbinde…";
    $("pauseBtn").innerHTML = icon(S.paused ? "play" : "pause");
    $("banner").innerHTML = S.error && !S.paused ? '<div class="banner">' + icon("alert") + "Agent-API nicht erreichbar: " + esc(S.error.message || S.error) + "</div>" : "";
    const st = S.status || {},
      s = snap();
    $("masterState").innerHTML = st.master_url ? (st.master_error ? SG.badge("Master gestört", "bad", "dot") : SG.badge("Master verbunden", "ok", "dot")) : "";
    $("masterState").title = st.master_error || st.master_url || "";
    const host = (s.server && s.server.hostname) || "Lokaler Terminalserver";
    $("host").textContent = host;
    $("host").title = host;
    const users = (s.sessions || []).filter((x) => x.user).length;
    $("navSessions").textContent = users || "";
    $("navSessions").hidden = !users;
    qsa(".nav a[data-route]").forEach((a) => a.classList.toggle("active", a.dataset.route === S.route));
  }

  // ------------------------------------------------------------ router --
  async function navigate() {
    let r = location.hash.replace(/^#\/?/, "").split("/")[0] || "overview";
    if (!views[r]) r = "overview";
    if (S.route === "policy" && r !== "policy" && SG.isDirty("agent-policy")) {
      if (!(await SG.confirm({ title: "Ungespeicherte Änderungen", message: "Die Policy enthält ungespeicherte Änderungen. Seite trotzdem verlassen?", confirm: "Verwerfen", danger: true }))) {
        history.replaceState(null, "", S.lastHash || "#/policy");
        return;
      }
      SG.setDirty("agent-policy", false);
    }
    S.route = r;
    S.lastHash = location.hash;
    const old = $("view"),
      fresh = old.cloneNode(false);
    old.replaceWith(fresh);
    const v = views[r];
    $("crumbs").innerHTML = "<h1>" + esc(v.title) + "</h1>";
    document.title = v.title + " · SessionGuard Agent";
    v.mount();
    SG.hydrateIcons($("view"));
    renderChrome();
    await tick(true);
    window.scrollTo(0, 0);
  }

  async function tick(force) {
    if (S.loading) return;
    if (!force && (S.paused || document.hidden)) return;
    S.loading = true;
    try {
      S.status = await api("/api/v1/status");
      recordMetrics();
      const v = views[S.route];
      if (v.load) await v.load(false);
      S.error = null;
      S.lastOK = new Date();
      v.update();
    } catch (e) {
      S.error = e;
    } finally {
      S.loading = false;
      renderChrome();
    }
  }

  async function init() {
    SG.hydrateIcons(document);
    SG.initTheme();
    SG.initNav();
    $("pauseBtn").onclick = () => {
      S.paused = !S.paused;
      renderChrome();
      if (!S.paused) tick(true);
    };
    $("refreshBtn").onclick = () => tick(true);
    try {
      S.me = await api("/api/v1/me");
      const u = (S.me && S.me.user) || {};
      const name = u.name || u.email || u.sub || "–";
      $("userName").textContent = name;
      $("userName").title = u.email || name;
      $("userAvatar").textContent = (name.match(/[A-Za-zÄÖÜäöü0-9]/g) || ["?"]).slice(0, 2).join("").toUpperCase();
    } catch (e) {
      /* older agents without /api/v1/me */
    }
    window.addEventListener("hashchange", navigate);
    document.addEventListener("visibilitychange", () => !document.hidden && tick(false));
    await navigate();
    setInterval(() => tick(false), REFRESH_MS);
  }
  init();
})();
