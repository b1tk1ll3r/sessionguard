/*
 * SessionGuard UI core (shared by Master and Agent). Plain JavaScript, no
 * build step. Exposes window.SG.
 *
 * Security: every value from the server is passed through SG.esc() before it
 * is inserted as HTML. Never build markup from unescaped data.
 */
(function () {
  "use strict";

  const SG = (window.SG = {});

  // ------------------------------------------------------------- basics --
  SG.$ = (id) => document.getElementById(id);
  SG.qs = (sel, root) => (root || document).querySelector(sel);
  SG.qsa = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  SG.esc = (s) =>
    String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  SG.debounce = (fn, ms) => {
    let t;
    return (...a) => {
      clearTimeout(t);
      t = setTimeout(() => fn(...a), ms);
    };
  };
  SG.lines = (v) =>
    String(v || "")
      .split("\n")
      .map((x) => x.trim())
      .filter(Boolean);
  SG.parseKV = (v) => {
    const o = {};
    SG.lines(v).forEach((x) => {
      const i = x.indexOf("=");
      if (i > 0) o[x.slice(0, i).trim()] = x.slice(i + 1).trim();
    });
    return o;
  };
  SG.kvText = (o) =>
    Object.entries(o || {})
      .map(([k, v]) => k + "=" + v)
      .join("\n");
  SG.clone = (o) => JSON.parse(JSON.stringify(o ?? null));

  // ------------------------------------------------------------ formats --
  const validDate = (v) => {
    if (!v) return null;
    const d = new Date(v);
    return Number.isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d;
  };
  SG.when = (v) => {
    const d = validDate(v);
    return d ? d.toLocaleString("de-DE") : "–";
  };
  SG.ago = (v) => {
    const d = validDate(v);
    if (!d) return "–";
    const s = Math.round((Date.now() - d.getTime()) / 1000);
    const abs = Math.abs(s);
    const fut = s < 0;
    let txt;
    if (abs < 10) return "gerade eben";
    if (abs < 60) txt = abs + " s";
    else if (abs < 3600) txt = Math.round(abs / 60) + " min";
    else if (abs < 86400) txt = Math.round(abs / 3600) + " h";
    else txt = Math.round(abs / 86400) + " d";
    return (fut ? "in " : "vor ") + txt;
  };
  // Relative time with the absolute timestamp as tooltip.
  SG.time = (v) => {
    const d = validDate(v);
    return d ? '<time title="' + SG.esc(d.toLocaleString("de-DE")) + '">' + SG.esc(SG.ago(v)) + "</time>" : "–";
  };
  SG.bytes = (n) => {
    if (!n) return "–";
    const u = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) {
      n /= 1024;
      i++;
    }
    return n.toFixed(i > 1 ? 1 : 0) + " " + u[i];
  };
  SG.duration = (sec) => {
    sec = Math.max(0, Math.round(sec || 0));
    if (sec < 60) return sec + " s";
    if (sec < 3600) return Math.round(sec / 60) + " min";
    const h = Math.floor(sec / 3600),
      m = Math.round((sec % 3600) / 60);
    if (h < 48) return h + " h " + m + " min";
    return Math.floor(h / 24) + " d " + (h % 24) + " h";
  };
  SG.since = (v) => {
    const d = validDate(v);
    return d ? SG.duration((Date.now() - d.getTime()) / 1000) : "–";
  };

  // -------------------------------------------------------------- icons --
  const ICONS = {
    grid: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
    server: '<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01"/>',
    users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
    layers: '<path d="m12 2 10 5-10 5L2 7z"/><path d="m2 17 10 5 10-5M2 12l10 5 10-5"/>',
    app: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01"/>',
    shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
    key: '<circle cx="7.5" cy="15.5" r="4.5"/><path d="m10.7 12.3 9.3-9.3M17 6l3 3M14 9l2 2"/>',
    file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h5"/>',
    bell: '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>',
    clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
    list: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z"/>',
    logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
    refresh: '<path d="M21 12a9 9 0 1 1-2.64-6.36L21 8"/><path d="M21 3v5h-5"/>',
    pause: '<rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>',
    play: '<path d="m6 3 14 9-14 9z"/>',
    search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/>',
    trash: '<path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>',
    x: '<path d="M18 6 6 18M6 6l12 12"/>',
    chevron: '<path d="m9 18 6-6-6-6"/>',
    back: '<path d="m15 18-6-6 6-6"/>',
    monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    alert: '<path d="m21.7 18-8-14a2 2 0 0 0-3.4 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3z"/><path d="M12 9v4M12 17h.01"/>',
    download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/>',
    menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
    message: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
    power: '<path d="M18.36 6.64a9 9 0 1 1-12.73 0M12 2v10"/>',
    activity: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
    copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
    eye: '<path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/>',
    sliders: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"/>',
    undo: '<path d="M3 7v6h6"/><path d="M21 17a9 9 0 0 0-15-6.7L3 13"/>',
    box: '<path d="M21 8 12 3 3 8v8l9 5 9-5z"/><path d="m3 8 9 5 9-5M12 13v8"/>',
    unplug: '<path d="m19 5 3-3M2 22l3-3M6.3 20.3a2.4 2.4 0 0 0 3.4 0L12 18l-6-6-2.3 2.3a2.4 2.4 0 0 0 0 3.4ZM7.5 13.5 10 11M10.5 16.5 13 14M12 6l6 6 2.3-2.3a2.4 2.4 0 0 0 0-3.4l-2.6-2.6a2.4 2.4 0 0 0-3.4 0Z"/>',
  };
  SG.icon = (name, cls) =>
    '<svg class="i' + (cls ? " " + cls : "") + '" viewBox="0 0 24 24" aria-hidden="true">' + (ICONS[name] || "") + "</svg>";
  // Replace <i data-icon="name"></i> placeholders in static HTML.
  SG.hydrateIcons = (root) =>
    SG.qsa("i[data-icon]", root).forEach((el) => {
      el.outerHTML = SG.icon(el.dataset.icon, el.className);
    });

  // ---------------------------------------------------------------- api --
  // body may be an object (sent as JSON). Throws Error(message) on failure;
  // a 401 redirects to the login page.
  SG.api = async (url, opts) => {
    opts = Object.assign({ headers: {} }, opts || {});
    if (opts.body !== undefined && typeof opts.body !== "string") {
      opts.body = JSON.stringify(opts.body);
      opts.headers["Content-Type"] = "application/json";
    } else if (typeof opts.body === "string") {
      opts.headers["Content-Type"] = opts.headers["Content-Type"] || "application/json";
    }
    opts.headers.Accept = "application/json";
    opts.credentials = "same-origin";
    const r = await fetch(url, opts);
    if (r.status === 401) {
      location.href = "/login";
      throw new Error("Anmeldung erforderlich");
    }
    const j = await r.json().catch(() => ({}));
    if (!r.ok) {
      const e = new Error((j && j.error) || r.statusText || "HTTP " + r.status);
      e.status = r.status;
      e.ref = j && j.ref;
      throw e;
    }
    return j;
  };

  // -------------------------------------------------------------- toast --
  SG.toast = (msg, kind) => {
    let root = SG.$("sg-toasts");
    if (!root) {
      root = document.createElement("div");
      root.id = "sg-toasts";
      root.className = "toasts";
      root.setAttribute("role", "status");
      root.setAttribute("aria-live", "polite");
      document.body.appendChild(root);
    }
    const el = document.createElement("div");
    el.className = "toast t-" + (kind || "ok");
    el.innerHTML =
      SG.icon(kind === "bad" ? "alert" : kind === "warn" ? "alert" : "check") +
      '<div class="grow">' +
      SG.esc(msg) +
      '</div><button class="btn ghost sm icon" aria-label="Schließen">' +
      SG.icon("x") +
      "</button>";
    el.querySelector("button").onclick = () => el.remove();
    root.appendChild(el);
    setTimeout(() => el.remove(), kind === "bad" ? 8000 : 4000);
  };
  SG.fail = (e) => SG.toast(((e && e.message) || String(e)) + (e && e.ref ? " (Ref " + e.ref + ")" : ""), "bad");

  // ------------------------------------------------------------- dialogs --
  const syncBodyLock = () => document.body.classList.toggle("dlg-open", !!document.querySelector("dialog[open]"));
  SG.openDialog = (dlg) => {
    if (!dlg.open) dlg.showModal();
    syncBodyLock();
  };
  SG.closeDialog = (dlg) => {
    if (dlg && dlg.open) dlg.close();
    syncBodyLock();
  };

  // Generic modal. opts: {title, subtitle, body(html), size, actions:[{label,
  // kind, value, icon}], onOpen(dlg), onAction(value, dlg) -> false keeps it
  // open, guard() -> false prevents closing}. Resolves with the action value
  // (or null when cancelled).
  SG.modal = (opts) =>
    new Promise((resolve) => {
      const dlg = document.createElement("dialog");
      dlg.className = "dlg" + (opts.size ? " " + opts.size : "");
      const acts = (opts.actions || [{ label: "Schließen", value: null }])
        .map(
          (a, i) =>
            '<button type="button" class="btn ' +
            (a.kind || "") +
            '" data-i="' +
            i +
            '">' +
            (a.icon ? SG.icon(a.icon) : "") +
            SG.esc(a.label) +
            "</button>",
        )
        .join("");
      dlg.innerHTML =
        '<form method="dialog" class="dlg-shell"><div class="dlg-head"><div><h3>' +
        SG.esc(opts.title || "") +
        "</h3>" +
        (opts.subtitle ? "<p>" + SG.esc(opts.subtitle) + "</p>" : "") +
        '</div><button type="button" class="btn ghost icon" data-close aria-label="Schließen">' +
        SG.icon("x") +
        '</button></div><div class="dlg-body">' +
        (opts.body || "") +
        '</div><div class="dlg-foot">' +
        (opts.footLeft ? '<div class="grow">' + opts.footLeft + "</div>" : "") +
        acts +
        "</div></form>";
      document.body.appendChild(dlg);
      let done = false;
      const finish = (v) => {
        if (done) return;
        done = true;
        SG.closeDialog(dlg);
        dlg.remove();
        syncBodyLock();
        resolve(v);
      };
      const tryClose = () => {
        if (opts.guard && opts.guard() === false) return;
        finish(null);
      };
      dlg.addEventListener("cancel", (e) => {
        e.preventDefault();
        tryClose();
      });
      dlg.querySelector("[data-close]").onclick = tryClose;
      dlg.querySelector("form").addEventListener("submit", (e) => e.preventDefault());
      SG.qsa(".dlg-foot [data-i]", dlg).forEach((b) => {
        b.onclick = async () => {
          const a = opts.actions[+b.dataset.i];
          if (a.value === null || a.value === undefined) return tryClose();
          if (opts.onAction) {
            b.disabled = true;
            try {
              const r = await opts.onAction(a.value, dlg);
              if (r === false) return;
            } catch (e) {
              SG.fail(e);
              return;
            } finally {
              b.disabled = false;
            }
          }
          finish(a.value);
        };
      });
      SG.openDialog(dlg);
      if (opts.onOpen) opts.onOpen(dlg);
      const first = dlg.querySelector(".dlg-body input:not([type=checkbox]), .dlg-body textarea, .dlg-body select");
      if (first) first.focus();
    });

  // Confirmation. opts: {title, message, confirm, danger, typeToConfirm}.
  SG.confirm = (opts) => {
    if (typeof opts === "string") opts = { message: opts };
    const type = opts.typeToConfirm;
    const body =
      '<p style="margin:0">' +
      SG.esc(opts.message || "") +
      "</p>" +
      (type
        ? '<label class="field" style="margin-top:14px"><span>Zur Bestätigung <code>' +
          SG.esc(type) +
          '</code> eingeben</span><input data-confirm autocomplete="off"></label>'
        : "");
    return SG.modal({
      title: opts.title || "Bestätigen",
      body,
      actions: [
        { label: "Abbrechen", value: null },
        { label: opts.confirm || "Bestätigen", kind: opts.danger ? "danger" : "primary", value: true },
      ],
      onAction: (v, dlg) => {
        if (type && dlg.querySelector("[data-confirm]").value !== type) {
          SG.toast("Bestätigungstext stimmt nicht überein", "warn");
          return false;
        }
      },
    }).then((v) => v === true);
  };

  // Text input. opts: {title, label, value, placeholder, multiline, confirm,
  // required, extra(html), maxLength}. Resolves with the string or null.
  SG.prompt = (opts) => {
    let value = null;
    const input = opts.multiline
      ? '<textarea data-prompt rows="4" maxlength="' +
        (opts.maxLength || 2000) +
        '" placeholder="' +
        SG.esc(opts.placeholder || "") +
        '" style="font-family:var(--font);font-size:13px">' +
        SG.esc(opts.value || "") +
        "</textarea>"
      : '<input data-prompt maxlength="' +
        (opts.maxLength || 500) +
        '" value="' +
        SG.esc(opts.value || "") +
        '" placeholder="' +
        SG.esc(opts.placeholder || "") +
        '">';
    return SG.modal({
      title: opts.title || "Eingabe",
      subtitle: opts.subtitle,
      body:
        (opts.message ? '<p style="margin:0 0 12px">' + SG.esc(opts.message) + "</p>" : "") +
        '<label class="field"><span>' +
        SG.esc(opts.label || "") +
        "</span>" +
        input +
        "</label>" +
        (opts.extra || ""),
      actions: [
        { label: "Abbrechen", value: null },
        { label: opts.confirm || "OK", kind: opts.danger ? "danger" : "primary", icon: opts.icon, value: "ok" },
      ],
      onAction: (v, dlg) => {
        value = dlg.querySelector("[data-prompt]").value;
        if (opts.required !== false && !value.trim()) {
          SG.toast("Bitte einen Text eingeben", "warn");
          return false;
        }
      },
    }).then((v) => (v === "ok" ? value : null));
  };

  // -------------------------------------------------------------- theme --
  SG.initTheme = () => {
    let saved = "";
    try {
      saved = localStorage.getItem("sessionguard-theme") || "";
    } catch (e) {}
    const prefersLight = window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches;
    const apply = (t) => {
      document.documentElement.dataset.theme = t;
      try {
        localStorage.setItem("sessionguard-theme", t);
      } catch (e) {}
      SG.qsa("[data-theme-toggle]").forEach((b) => {
        b.innerHTML = SG.icon(t === "light" ? "moon" : "sun") + (b.dataset.themeToggle === "label" ? (t === "light" ? " Dunkel" : " Hell") : "");
        b.title = t === "light" ? "Dunkles Theme" : "Helles Theme";
      });
    };
    apply(saved || (prefersLight ? "light" : "dark"));
    document.addEventListener("click", (e) => {
      if (e.target.closest("[data-theme-toggle]")) apply(document.documentElement.dataset.theme === "light" ? "dark" : "light");
    });
  };
  SG.initNav = () => {
    document.addEventListener("click", (e) => {
      if (e.target.closest("[data-nav-toggle]")) document.body.classList.toggle("nav-open");
      else if (e.target.closest(".scrim") || e.target.closest(".nav a")) document.body.classList.remove("nav-open");
    });
  };

  // ------------------------------------------------------------- tables --
  // Per-table UI state (search text, sort) survives re-renders/refreshes.
  const tableState = {};
  const tableRender = {};
  SG.tstate = (id, defaults) => (tableState[id] = tableState[id] || Object.assign({ q: "", sort: null, dir: 1 }, defaults || {}));

  // cfg: {id, columns:[{key,label,html(row),value(row),sort:false,cls,th}],
  // rows, empty, search(row)->string, rowAttrs(row)->string, max, onRender}
  SG.table = (cfg) => {
    const st = SG.tstate(cfg.id, cfg.defaultSort ? { sort: cfg.defaultSort, dir: cfg.defaultDir || 1 } : null);
    let rows = cfg.rows || [];
    const total = rows.length;
    if (st.q && cfg.search) {
      const q = st.q.toLowerCase();
      rows = rows.filter((r) => String(cfg.search(r) || "").toLowerCase().includes(q));
    }
    if (st.sort) {
      const col = cfg.columns.find((c) => c.key === st.sort);
      if (col) {
        const val = col.value || ((r) => r[col.key]);
        rows = rows.slice().sort((a, b) => {
          let x = val(a),
            y = val(b);
          if (x == null) x = "";
          if (y == null) y = "";
          if (typeof x === "number" && typeof y === "number") return (x - y) * st.dir;
          return String(x).localeCompare(String(y), "de", { numeric: true, sensitivity: "base" }) * st.dir;
        });
      }
    }
    const shown = cfg.max ? rows.slice(0, cfg.max) : rows;
    if (!total) return '<div class="empty">' + SG.icon(cfg.emptyIcon || "list") + SG.esc(cfg.empty || "Keine Einträge.") + "</div>";
    const head = cfg.columns
      .map((c) => {
        const sortable = c.sort !== false && c.label;
        const sorted = st.sort === c.key;
        return (
          "<th" +
          (c.th ? " " + c.th : "") +
          ' class="' +
          (c.cls || "") +
          (sortable ? " sortable" : "") +
          (sorted ? " sorted" : "") +
          '"' +
          (sortable ? ' data-sort-table="' + SG.esc(cfg.id) + '" data-sort-key="' + SG.esc(c.key) + '"' : "") +
          ">" +
          (c.labelHtml || SG.esc(c.label || "")) +
          (sortable ? '<span class="arrow">' + (sorted ? (st.dir > 0 ? "▲" : "▼") : "↕") + "</span>" : "") +
          "</th>"
        );
      })
      .join("");
    const body = shown.length
      ? shown
          .map(
            (r) =>
              "<tr" +
              (cfg.rowAttrs ? " " + cfg.rowAttrs(r) : "") +
              ">" +
              cfg.columns.map((c) => '<td class="' + (c.cls || "") + '">' + (c.html ? c.html(r) : SG.esc(r[c.key] ?? "–")) + "</td>").join("") +
              "</tr>",
          )
          .join("")
      : '<tr><td colspan="' + cfg.columns.length + '"><div class="empty">Keine Treffer für „' + SG.esc(st.q) + "“.</div></td></tr>";
    const foot =
      cfg.foot !== false && (st.q || (cfg.max && rows.length > cfg.max) || total > 10)
        ? '<div class="table-foot"><span>' +
          (rows.length === total ? total + " Einträge" : rows.length + " von " + total + " Einträgen") +
          (cfg.max && rows.length > cfg.max ? " · die ersten " + cfg.max + " angezeigt" : "") +
          "</span></div>"
        : "";
    return '<div class="table-wrap"><table class="table"><thead><tr>' + head + "</tr></thead><tbody>" + body + "</tbody></table></div>" + foot;
  };
  // Register a re-render callback for a table id (called on sort/search).
  SG.onTable = (id, fn) => (tableRender[id] = fn);
  document.addEventListener("click", (e) => {
    const th = e.target.closest("th[data-sort-table]");
    if (!th) return;
    const st = SG.tstate(th.dataset.sortTable);
    if (st.sort === th.dataset.sortKey) st.dir = -st.dir;
    else {
      st.sort = th.dataset.sortKey;
      st.dir = 1;
    }
    if (tableRender[th.dataset.sortTable]) tableRender[th.dataset.sortTable]();
  });
  // <input data-table-search="id"> filters a registered table.
  document.addEventListener(
    "input",
    SG.debounce((e) => {
      const inp = e.target.closest && e.target.closest("[data-table-search]");
      if (!inp) return;
      SG.tstate(inp.dataset.tableSearch).q = inp.value.trim();
      if (tableRender[inp.dataset.tableSearch]) tableRender[inp.dataset.tableSearch]();
    }, 120),
  );
  SG.searchBox = (id, placeholder) =>
    '<label class="search">' +
    SG.icon("search") +
    '<input class="input" type="search" data-table-search="' +
    SG.esc(id) +
    '" placeholder="' +
    SG.esc(placeholder || "Suchen…") +
    '" value="' +
    SG.esc(SG.tstate(id).q) +
    '" aria-label="' +
    SG.esc(placeholder || "Suchen") +
    '"></label>';

  // CSV export (Excel-compatible: UTF-8 BOM, semicolon separator).
  SG.csv = (filename, columns, rows) => {
    const cell = (v) => {
      v = v == null ? "" : String(v);
      // Neutralise spreadsheet formula injection.
      if (/^[=+\-@\t\r]/.test(v)) v = "'" + v;
      return '"' + v.replace(/"/g, '""') + '"';
    };
    const out = [columns.map((c) => cell(c.label)).join(";")]
      .concat(rows.map((r) => columns.map((c) => cell(c.csv ? c.csv(r) : r[c.key])).join(";")))
      .join("\r\n");
    const blob = new Blob(["﻿" + out], { type: "text/csv;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    setTimeout(() => {
      URL.revokeObjectURL(a.href);
      a.remove();
    }, 500);
  };

  // ------------------------------------------------------------ visuals --
  SG.badge = (text, kind, icon) => '<span class="badge ' + (kind || "") + '">' + (icon === "dot" ? '<i class="dot"></i>' : icon ? SG.icon(icon) : "") + SG.esc(text) + "</span>";
  SG.meter = (label, pct, text, thresholds) => {
    const t = thresholds || [75, 90];
    pct = Math.max(0, Math.min(100, Number(pct) || 0));
    const cls = pct >= t[1] ? "bad" : pct >= t[0] ? "warn" : "";
    return (
      '<div class="meter ' +
      cls +
      '"><div class="meter-top"><span class="muted">' +
      SG.esc(label) +
      '</span><span class="strong">' +
      SG.esc(text ?? Math.round(pct) + " %") +
      '</span></div><div class="meter-bar"><i style="width:' +
      pct.toFixed(1) +
      '%"></i></div></div>'
    );
  };
  // Health is "higher is better".
  SG.healthMeter = (score) => {
    score = Number(score) || 0;
    const cls = score < 50 ? "bad" : score < 70 ? "warn" : "";
    return (
      '<div class="meter ' +
      cls +
      '"><div class="meter-top"><span class="muted">Health</span><span class="strong">' +
      score +
      '</span></div><div class="meter-bar"><i style="width:' +
      Math.max(0, Math.min(100, score)) +
      '%"></i></div></div>'
    );
  };
  SG.spark = (values, cls, max) => {
    const v = (values || []).filter((x) => Number.isFinite(x));
    if (v.length < 2) return '<svg class="spark ' + (cls || "") + '" viewBox="0 0 100 34" preserveAspectRatio="none"></svg>';
    const m = max || Math.max(100, ...v);
    const pts = v.map((x, i) => [(i / (v.length - 1)) * 100, 32 - (x / m) * 30]);
    const line = "M" + pts.map((p) => p[0].toFixed(2) + " " + p[1].toFixed(2)).join(" L");
    return (
      '<svg class="spark ' +
      (cls || "") +
      '" viewBox="0 0 100 34" preserveAspectRatio="none" aria-hidden="true"><path class="area" d="' +
      line +
      ' L100 34 L0 34 Z"/><path class="line" d="' +
      line +
      '" vector-effect="non-scaling-stroke"/></svg>'
    );
  };
  SG.empty = (text, icon) => '<div class="empty">' + SG.icon(icon || "list") + SG.esc(text) + "</div>";
  SG.json = (o) => '<pre class="json">' + SG.esc(JSON.stringify(o, null, 2)) + "</pre>";

  // Copy to clipboard for elements with data-copy="text".
  document.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-copy]");
    if (!b) return;
    try {
      await navigator.clipboard.writeText(b.dataset.copy);
      SG.toast("In die Zwischenablage kopiert");
    } catch (err) {
      SG.toast("Kopieren nicht möglich", "warn");
    }
  });

  // ------------------------------------------------------------- dirty --
  // Registry of unsaved editors; warns before leaving the page.
  const dirty = new Set();
  SG.setDirty = (key, on) => (on ? dirty.add(key) : dirty.delete(key));
  SG.isDirty = (key) => (key ? dirty.has(key) : dirty.size > 0);
  window.addEventListener("beforeunload", (e) => {
    if (dirty.size) {
      e.preventDefault();
      e.returnValue = "";
    }
  });
})();
