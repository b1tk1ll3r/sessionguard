/*
 * Shared policy editor for SessionGuard policies (profiles, sessions,
 * cleanup, profile folders, templates). Used by the Master (server override,
 * farm policy, global policy) and the local Agent UI.
 *
 *   const ed = SG.PolicyEditor(container, policy, { onDirty(bool) });
 *   ed.collect()  -> policy body for PUT
 *   ed.isDirty(), ed.reset(policy)
 *
 * All inputs are scoped to the container, so several editors can coexist.
 */
(function () {
  "use strict";
  const SG = window.SG;
  const esc = SG.esc;

  const SECTIONS = [
    {
      key: "profiles",
      title: "Profil-Sicherung & Wiederherstellung",
      icon: "box",
      fields: [
        ["enabled", "bool", "Profil-Synchronisation aktiv"],
        ["backup_on_logoff", "bool", "Nach Logoff sichern"],
        ["restore_on_logon", "bool", "Bei Logon wiederherstellen"],
        ["store_root", "str", "Profil-Store (UNC oder lokaler Pfad)", { wide: true, placeholder: "\\\\fileserver\\SessionGuardProfiles" }],
        ["backup_delay_seconds", "int", "Backup-Verzögerung (s)", { min: 1, def: 5 }],
        ["retry_seconds", "int", "Retry (s)", { min: 1, def: 60 }],
        ["restore_window_seconds", "int", "Restore-Fenster (s)", { min: 10, def: 120 }],
        ["keep_versions", "int", "Historische Versionen", { min: 0, max: 50, def: 2, zero: true }],
        ["exclude_users", "lines", "Benutzer ausschließen", { help: "Ein Eintrag pro Zeile" }],
        ["exclude_sids", "lines", "SIDs ausschließen", { help: "Ein Eintrag pro Zeile" }],
      ],
      folders: true,
    },
    {
      key: "sessions",
      title: "Sitzungsrichtlinie",
      icon: "users",
      fields: [
        ["control_enabled", "bool", "Administrative Sitzungssteuerung erlauben"],
        ["disconnected_logoff_enabled", "bool", "Getrennte Sitzungen automatisch abmelden"],
        ["disconnected_timeout_seconds", "int", "Timeout getrennte Sitzung (s, min. 60)", { min: 60, def: 3600 }],
        ["exclude_users", "lines", "Vom Auto-Logoff ausgeschlossene Benutzer"],
        ["exclude_sids", "lines", "Vom Auto-Logoff ausgeschlossene SIDs"],
      ],
    },
    {
      key: "cleanup",
      title: "Profil-Cleanup",
      icon: "trash",
      fields: [
        ["enabled", "bool", "Cleanup aktiv"],
        ["dry_run", "bool", "Dry-Run (nur protokollieren, nichts löschen)"],
        ["grace_seconds", "int", "Grace (s)", { min: 1, def: 600 }],
        ["poll_seconds", "int", "Polling (s)", { min: 2, def: 10 }],
        ["retry_seconds", "int", "Retry (s)", { min: 1, def: 60 }],
        ["exclude_users", "lines", "Benutzer ausschließen"],
        ["exclude_sids", "lines", "SIDs ausschließen"],
        ["allowed_profile_roots", "lines", "Erlaubte Profil-Roots", { help: "z. B. C:\\Users" }],
      ],
    },
  ];

  const TEMPLATE_KINDS = [
    ["file", "Datei"],
    ["directory", "Ordner"],
    ["url", "URL-Verknüpfung"],
    ["shortcut", "Verknüpfung (.lnk)"],
  ];

  function fieldHTML(section, f, data) {
    const [name, type, label, o] = [f[0], f[1], f[2], f[3] || {}];
    const id = section + "." + name;
    let v = (data || {})[name];
    if (type === "bool") {
      return (
        '<label class="switch"><input type="checkbox" data-pk="' +
        id +
        '" data-pt="bool"' +
        (v ? " checked" : "") +
        "> " +
        esc(label) +
        "</label>"
      );
    }
    if (type === "int") {
      if (v === undefined || v === null || (v === 0 && !o.zero)) v = o.def;
      return (
        '<label class="field"><span>' +
        esc(label) +
        '</span><input type="number" data-pk="' +
        id +
        '" data-pt="int"' +
        (o.min !== undefined ? ' min="' + o.min + '"' : "") +
        (o.max !== undefined ? ' max="' + o.max + '"' : "") +
        ' value="' +
        esc(v) +
        '"></label>'
      );
    }
    if (type === "lines") {
      return (
        '<label class="field"><span>' +
        esc(label) +
        '</span><textarea data-pk="' +
        id +
        '" data-pt="lines" rows="3">' +
        esc((v || []).join("\n")) +
        "</textarea>" +
        (o.help ? "<small>" + esc(o.help) + "</small>" : "") +
        "</label>"
      );
    }
    return (
      '<label class="field' +
      (o.wide ? " wide" : "") +
      '"><span>' +
      esc(label) +
      '</span><input data-pk="' +
      id +
      '" data-pt="str" value="' +
      esc(v || "") +
      '" placeholder="' +
      esc(o.placeholder || "") +
      '"></label>'
    );
  }

  function folderHTML(f, i) {
    return (
      '<div class="item" data-folder><div class="item-head"><strong>' +
      esc(f.path || "Ordner " + (i + 1)) +
      '</strong><button type="button" class="btn sm danger" data-pe="remove-folder" data-index="' +
      i +
      '">' +
      SG.icon("trash") +
      'Entfernen</button></div><div class="fields"><label class="field"><span>Pfad relativ zum Profil</span><input data-ff="path" value="' +
      esc(f.path || "") +
      '" placeholder="AppData\\Roaming\\Hersteller"></label><label class="field"><span>Ausschlüsse (eine pro Zeile; * oder Pfad/**)</span><textarea data-ff="exclude" rows="3">' +
      esc((f.exclude_globs || []).join("\n")) +
      "</textarea></label></div></div>"
    );
  }

  function templateSpecific(t) {
    const k = (t.kind || "file").toLowerCase();
    if (k === "directory") return '<div class="help">Legt nur den Ordner an.</div>';
    if (k === "url") return '<label class="field"><span>URL</span><input data-tf="url" value="' + esc(t.url || "") + '" placeholder="https://"></label>';
    if (k === "shortcut") {
      const s = t.shortcut || {};
      return (
        '<div class="fields"><label class="field"><span>Zielprogramm</span><input data-tf="shortcut.target" value="' +
        esc(s.target || "") +
        '"></label><label class="field"><span>Argumente</span><input data-tf="shortcut.arguments" value="' +
        esc(s.arguments || "") +
        '"></label><label class="field"><span>Arbeitsverzeichnis</span><input data-tf="shortcut.working_directory" value="' +
        esc(s.working_directory || "") +
        '"></label><label class="field"><span>Icon</span><input data-tf="shortcut.icon_location" value="' +
        esc(s.icon_location || "") +
        '"></label><label class="field wide"><span>Beschreibung</span><input data-tf="shortcut.description" value="' +
        esc(s.description || "") +
        '"></label></div>'
      );
    }
    return (
      '<label class="field"><span>Quelldatei / UNC-Pfad</span><input data-tf="source" value="' +
      esc(t.source || "") +
      '"><small>Alternativ Inhalt direkt angeben. Quellen müssen auf dem Agent in <code>local_guard.allowed_template_source_roots</code> erlaubt sein.</small></label><div class="fields"><label class="field"><span>Inline-Inhalt</span><textarea data-tf="content" rows="4">' +
      esc(t.content || "") +
      '</textarea></label><label class="field"><span>Inline Base64</span><textarea data-tf="content_base64" rows="4">' +
      esc(t.content_base64 || "") +
      "</textarea></label></div>"
    );
  }

  function templateHTML(t, i) {
    return (
      '<div class="item" data-template><div class="item-head"><strong>' +
      esc(t.id || "Template " + (i + 1)) +
      '</strong><button type="button" class="btn sm danger" data-pe="remove-template" data-index="' +
      i +
      '">' +
      SG.icon("trash") +
      'Entfernen</button></div><div class="fields"><label class="field"><span>ID</span><input data-tf="id" value="' +
      esc(t.id || "") +
      '"></label><label class="field"><span>Typ</span><select data-tf="kind" data-pe-kind>' +
      TEMPLATE_KINDS.map(([v, l]) => '<option value="' + v + '"' + ((t.kind || "file") === v ? " selected" : "") + ">" + l + "</option>").join("") +
      '</select></label><label class="field"><span>Ziel relativ zum Profil</span><input data-tf="target" value="' +
      esc(t.target || "") +
      '"></label></div><label class="switch"><input type="checkbox" data-tf="overwrite"' +
      (t.overwrite ? " checked" : "") +
      "> Aktualisieren/überschreiben</label>" +
      templateSpecific(t) +
      "</div>"
    );
  }

  function readTemplate(card) {
    const g = (n) => {
      const e = card.querySelector('[data-tf="' + n + '"]');
      return e ? e.value : "";
    };
    const k = g("kind") || "file";
    const t = { id: g("id").trim(), kind: k, target: g("target").trim(), overwrite: !!card.querySelector('[data-tf="overwrite"]:checked') };
    if (k === "file") {
      t.source = g("source").trim();
      t.content = g("content");
      t.content_base64 = g("content_base64").trim();
    } else if (k === "url") t.url = g("url").trim();
    else if (k === "shortcut")
      t.shortcut = {
        target: g("shortcut.target").trim(),
        arguments: g("shortcut.arguments"),
        working_directory: g("shortcut.working_directory").trim(),
        icon_location: g("shortcut.icon_location").trim(),
        description: g("shortcut.description"),
      };
    return t;
  }

  SG.PolicyEditor = function (root, policy, opts) {
    opts = opts || {};
    let dirty = false;
    const setDirty = (v) => {
      if (dirty === v) return;
      dirty = v;
      if (opts.onDirty) opts.onDirty(v);
    };

    function folders() {
      return SG.qsa("[data-folder]", root)
        .map((c) => ({ path: c.querySelector('[data-ff="path"]').value.trim(), exclude_globs: SG.lines(c.querySelector('[data-ff="exclude"]').value) }))
        .filter((x) => x.path);
    }
    function templates() {
      return SG.qsa("[data-template]", root).map(readTemplate);
    }
    function renderFolders(list) {
      SG.qs("[data-pe-folders]", root).innerHTML = list.length ? list.map(folderHTML).join("") : '<div class="help">Keine Profilordner konfiguriert.</div>';
    }
    function renderTemplates(list) {
      SG.qs("[data-pe-templates]", root).innerHTML = list.length ? list.map(templateHTML).join("") : '<div class="help">Keine Templates konfiguriert.</div>';
    }

    function render(p) {
      p = p || {};
      root.innerHTML =
        '<div class="stack">' +
        SECTIONS.map((s) => {
          const data = p[s.key] || {};
          const bools = s.fields.filter((f) => f[1] === "bool");
          const others = s.fields.filter((f) => f[1] !== "bool");
          return (
            '<details class="section" open><summary>' +
            SG.icon(s.icon) +
            esc(s.title) +
            '</summary><div class="section-body"><div class="row" style="gap:18px">' +
            bools.map((f) => fieldHTML(s.key, f, data)).join("") +
            '</div><div class="fields">' +
            others.map((f) => fieldHTML(s.key, f, data)).join("") +
            "</div>" +
            (s.folders
              ? '<div class="row between"><strong>Zu sichernde Profilordner</strong><button type="button" class="btn sm" data-pe="add-folder">' +
                SG.icon("plus") +
                'Ordner</button></div><div class="list" data-pe-folders></div><div class="note">Gesichert werden nur konfigurierte Teilbäume. Reparse-Points/Symlinks werden nicht verfolgt; Cleanup beginnt nach einem Logoff erst nach erfolgreichem Backup.</div>'
              : "") +
            "</div></details>"
          );
        }).join("") +
        '<details class="section" open><summary>' +
        SG.icon("file") +
        'Templates<span class="help" style="font-weight:400">Dateien, Ordner und Verknüpfungen im Benutzerprofil</span></summary><div class="section-body"><div class="row end"><button type="button" class="btn sm" data-pe="add-template">' +
        SG.icon("plus") +
        'Template</button></div><div class="list" data-pe-templates></div></div></details></div>';
      renderFolders(SG.clone((p.profiles && p.profiles.folders) || []));
      renderTemplates(SG.clone(p.templates || []));
      setDirty(false);
    }

    root.addEventListener("input", (e) => {
      if (e.target.matches("input,textarea,select")) setDirty(true);
    });
    root.addEventListener("change", (e) => {
      if (e.target.matches("[data-pe-kind]")) {
        const list = templates();
        renderTemplates(list);
      }
      if (e.target.matches("input,textarea,select")) setDirty(true);
    });
    root.addEventListener("click", (e) => {
      const b = e.target.closest("[data-pe]");
      if (!b) return;
      const a = b.dataset.pe;
      if (a === "add-folder") {
        const l = folders();
        l.push({ path: "AppData\\Roaming\\Hersteller", exclude_globs: ["Cache/**"] });
        renderFolders(l);
      } else if (a === "remove-folder") {
        const all = SG.qsa("[data-folder]", root).map((c) => ({ path: c.querySelector('[data-ff="path"]').value.trim(), exclude_globs: SG.lines(c.querySelector('[data-ff="exclude"]').value) }));
        all.splice(+b.dataset.index, 1);
        renderFolders(all);
      } else if (a === "add-template") {
        const l = templates();
        l.push({ id: "neues-template", kind: "file", target: "Desktop\\Beispiel.txt", source: "", content: "", content_base64: "", overwrite: true });
        renderTemplates(l);
      } else if (a === "remove-template") {
        const l = templates();
        l.splice(+b.dataset.index, 1);
        renderTemplates(l);
      } else return;
      setDirty(true);
    });

    render(policy);

    return {
      collect() {
        const out = { profiles: {}, sessions: {}, cleanup: {} };
        SG.qsa("[data-pk]", root).forEach((el) => {
          const [sec, name] = el.dataset.pk.split(".");
          const t = el.dataset.pt;
          out[sec][name] = t === "bool" ? el.checked : t === "int" ? +el.value : t === "lines" ? SG.lines(el.value) : el.value.trim();
        });
        out.profiles.folders = folders();
        out.templates = templates();
        return out;
      },
      isDirty: () => dirty,
      reset: render,
      markClean: () => setDirty(false),
    };
  };
})();
