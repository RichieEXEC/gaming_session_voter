// Server vykreslí obě desky i výsledky. Skript jen zpříjemňuje klikání a
// obsluhuje hledání her. Bez něj jsou výsledky pořád čitelné, jen se nedá
// klikat ani hledat (hlas ale odeslat lze, drží hodnoty ze serveru).
(function () {
  "use strict";

  var VALS = ["no", "yes", "maybe"];

  var GLYPH = {
    yes: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 6.4 4.6 9 10 3"/></svg>',
    maybe: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M1.5 7.2c1-2.2 2.2-2.2 3.2 0s2.2 2.2 3.2 0"/></svg>',
    no: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3.5 6h5"/></svg>'
  };

  var EXT_ICON = '<svg class="ext" width="9" height="9" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4.5 2.5H2.5v7h7v-2"/><path d="M7 2.5h2.5V5"/><path d="M9.5 2.5 5.5 6.5"/></svg>';

  var toastEl = document.getElementById("toast");
  var toastTimer;
  function toast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.classList.add("on");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toastEl.classList.remove("on"); }, 1900);
  }
  if (toastEl && toastEl.classList.contains("on")) {
    toastTimer = setTimeout(function () { toastEl.classList.remove("on"); }, 2600);
  }

  /* ---------- náhradní obal (stejné jako na serveru) ---------- */

  function hue(name) {
    var h = 0;
    for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360;
    return h < 0 ? h + 360 : h;
  }
  function initials(name) {
    var words = (name.match(/[\p{L}\p{N}]+/gu) || []);
    if (!words.length) return "?";
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  function coverEl(game, size) {
    var c = document.createElement("span");
    c.className = "cover cover-" + size;
    c.style.setProperty("--h", hue(game.name));
    c.setAttribute("aria-hidden", "true");
    if (game.cover) {
      var img = document.createElement("img");
      img.className = "cover-img";
      img.src = "https://images.igdb.com/igdb/image/upload/t_cover_small/" + game.cover + ".jpg";
      img.alt = "";
      img.loading = "lazy";
      img.onerror = function () { img.remove(); };
      c.appendChild(img);
    }
    var mono = document.createElement("span");
    mono.className = "cover-mono";
    mono.textContent = initials(game.name);
    c.appendChild(mono);
    return c;
  }

  /* ---------- klikání v tabulkách ---------- */

  var form = document.getElementById("vote-form");
  var marked = document.getElementById("marked");
  var markedTpl = marked ? marked.getAttribute("data-tpl") : null;

  function wordFor(board, val) {
    var p = board === "game" ? "g" : "l";
    return form.getAttribute("data-" + p + val) || val;
  }

  function updateMarked() {
    if (!marked || !markedTpl) return;
    var n = 0;
    document.querySelectorAll(".cell.live").forEach(function (c) {
      if (c.getAttribute("data-v") !== "no") n++;
    });
    marked.textContent = markedTpl.replace("%d", n);
  }

  // Klikání na buňky je delegované na dokument: buňky jsou mimo formulář.
  document.addEventListener("click", function (e) {
    var cell = e.target.closest(".cell.live");
    if (!cell || !form) return;

    var id = cell.getAttribute("data-opt");
    var next = VALS[(VALS.indexOf(cell.getAttribute("data-v")) + 1) % VALS.length];
    cell.setAttribute("data-v", next);
    cell.innerHTML = GLYPH[next];

    var input = document.getElementById("in-" + id);
    if (input) input.value = next;

    var board = cell.getAttribute("data-board");
    var prefix = cell.getAttribute("data-prefix") || "";
    cell.setAttribute("aria-label", prefix + ": " + wordFor(board, next) + ". " + (form.getAttribute("data-hint") || ""));
    updateMarked();
  });

  updateMarked();

  // Přezdívka se propisuje do hlaviček sloupce "Ty" v obou tabulkách.
  var nick = document.getElementById("nick");
  var mynames = document.querySelectorAll("[data-myname]");
  if (nick && mynames.length) {
    var fallback = mynames[0].textContent;
    nick.addEventListener("input", function () {
      var v = nick.value.trim();
      mynames.forEach(function (el) {
        el.textContent = v || fallback;
        el.setAttribute("title", v);
      });
    });
  }

  /* ---------- odkaz ---------- */

  var copy = document.getElementById("copy-link");
  if (copy) {
    copy.addEventListener("click", function () {
      var url = location.origin + location.pathname;
      var ok = copy.getAttribute("data-ok");
      var fail = copy.getAttribute("data-fail");
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(url).then(function () { toast(ok); }, function () { toast(fail); });
      } else {
        toast(fail);
      }
    });
  }

  /* ---------- hledání her ---------- */

  var finder = document.querySelector(".finder");
  var qInput = document.getElementById("game-q");
  var resultsEl = document.getElementById("game-results");

  if (finder && qInput && resultsEl && finder.getAttribute("data-on") === "1") {
    var slug = finder.getAttribute("data-slug");
    var searchTimer, lastQuery = "";

    function hintRow(text) {
      resultsEl.innerHTML = "";
      var d = document.createElement("div");
      d.className = "hint";
      d.textContent = text;
      resultsEl.appendChild(d);
      resultsEl.hidden = false;
    }

    function renderResults(games) {
      resultsEl.innerHTML = "";
      if (!games.length) {
        hintRow(finder.getAttribute("data-none"));
        return;
      }
      games.forEach(function (g) {
        var row = document.createElement("div");
        row.className = "res";
        row.appendChild(coverEl(g, "sm"));

        var body = document.createElement("span");
        body.className = "res-body";
        var name = document.createElement("span");
        name.className = "res-name";
        if (g.igdbUrl) {
          var link = document.createElement("a");
          link.href = g.igdbUrl;
          link.target = "_blank";
          link.rel = "noopener noreferrer";
          link.textContent = g.name;
          link.insertAdjacentHTML("beforeend", EXT_ICON);
          name.appendChild(link);
        } else {
          name.textContent = g.name;
        }
        var meta = document.createElement("span");
        meta.className = "res-meta";
        meta.textContent = g.meta;
        body.appendChild(name);
        body.appendChild(meta);
        row.appendChild(body);

        if (g.in) {
          var tag = document.createElement("span");
          tag.className = "res-add";
          tag.textContent = finder.getAttribute("data-already");
          row.appendChild(tag);
        } else {
          var f = document.createElement("form");
          f.className = "res-form";
          f.method = "post";
          f.action = "/p/" + slug + "/games";
          addHidden(f, "igdb_id", g.igdbId);
          addHidden(f, "name", g.name);
          addHidden(f, "year", g.year);
          addHidden(f, "genre", g.genre);
          addHidden(f, "max", g.max);
          addHidden(f, "cover", g.cover);
          addHidden(f, "slug", g.slug);
          var btn = document.createElement("button");
          btn.type = "submit";
          btn.className = "res-add";
          btn.textContent = finder.getAttribute("data-add");
          f.appendChild(btn);
          row.appendChild(f);
        }
        resultsEl.appendChild(row);
      });
      resultsEl.hidden = false;
    }

    function addHidden(f, name, val) {
      var i = document.createElement("input");
      i.type = "hidden";
      i.name = name;
      i.value = val == null ? "" : String(val);
      f.appendChild(i);
    }

    function run(q) {
      fetch("/p/" + slug + "/games/search?q=" + encodeURIComponent(q), {
        headers: { "Accept": "application/json" }
      }).then(function (r) { return r.json(); }).then(function (data) {
        if (qInput.value.trim() !== q) return; // dotaz se mezitím změnil
        if (data.disabled) { hintRow(finder.getAttribute("data-disabled")); return; }
        if (data.error) { hintRow(data.error || finder.getAttribute("data-error")); return; }
        renderResults(data.games || []);
      }).catch(function () {
        hintRow(finder.getAttribute("data-error"));
      });
    }

    qInput.addEventListener("input", function () {
      var q = qInput.value.trim();
      clearTimeout(searchTimer);
      if (q.length < 2) { resultsEl.hidden = true; resultsEl.innerHTML = ""; lastQuery = ""; return; }
      if (q === lastQuery) return;
      lastQuery = q;
      searchTimer = setTimeout(function () { run(q); }, 220);
    });

    qInput.addEventListener("keydown", function (e) {
      if (e.key === "Escape") { qInput.value = ""; resultsEl.hidden = true; resultsEl.innerHTML = ""; lastQuery = ""; }
    });
  }

  /* ---------- potvrzení mazání hry s hlasy ---------- */

  var confirmDlg = document.getElementById("confirm-delete");
  if (confirmDlg && typeof confirmDlg.showModal === "function") {
    var titleEl = document.getElementById("confirm-title");
    var bodyEl = document.getElementById("confirm-body");
    var defaultTitle = titleEl ? titleEl.textContent : "";
    var pendingOk = null;

    // Nadpis i text bere z prvku, který potvrzení vyvolal, takže dialog
    // slouží mazání hry i odebrání sebe sama.
    function ask(el, onOk) {
      if (titleEl) titleEl.textContent = el.getAttribute("data-confirm-title") || defaultTitle;
      bodyEl.textContent = el.getAttribute("data-confirm-body") || "";
      pendingOk = onOk;
      confirmDlg.showModal();
    }

    // Mazání hry s hlasy: odchycený submit formuláře (data-confirm).
    document.addEventListener("submit", function (e) {
      var f = e.target;
      if (!f.classList || !f.classList.contains("g-drop-form")) return;
      if (f.getAttribute("data-confirm") !== "1") return;
      e.preventDefault();
      ask(f, function () { f.submit(); }); // form.submit() nespouští submit event
    });

    // Odebrání sebe: tlačítko s formaction uvnitř hlasovacího formuláře.
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-confirm-self]");
      if (!btn) return;
      e.preventDefault();
      var form = btn.form;
      ask(btn, function () { if (form) form.requestSubmit(btn); });
    });

    confirmDlg.querySelector("[data-confirm-cancel]").addEventListener("click", function () {
      confirmDlg.close();
    });
    confirmDlg.querySelector("[data-confirm-ok]").addEventListener("click", function () {
      var ok = pendingOk;
      confirmDlg.close();
      if (ok) ok();
    });
    confirmDlg.addEventListener("close", function () { pendingOk = null; });
  }

  /* ---------- ruční počet hráčů ---------- */

  document.addEventListener("click", function (e) {
    var edit = e.target.closest("[data-max-edit]");
    if (edit) {
      var wrap = edit.closest(".max-wrap");
      edit.hidden = true;
      var form = wrap.querySelector(".max-form");
      form.hidden = false;
      var inp = form.querySelector(".max-in");
      if (inp) { inp.focus(); inp.select(); }
      return;
    }
    var cancel = e.target.closest("[data-max-cancel]");
    if (cancel) {
      var w = cancel.closest(".max-wrap");
      w.querySelector(".max-form").hidden = true;
      w.querySelector(".max-edit").hidden = false;
    }
  });

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && e.target.classList && e.target.classList.contains("max-in")) {
      var w = e.target.closest(".max-wrap");
      w.querySelector(".max-form").hidden = true;
      w.querySelector(".max-edit").hidden = false;
    }
  });

  /* ---------- zakládání (stránka nového sezení) ---------- */

  function addDays(iso, n) {
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || "");
    if (!m) return "";
    var d = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3]));
    if (isNaN(d.getTime())) return "";
    d.setUTCDate(d.getUTCDate() + n);
    return d.toISOString().slice(0, 10);
  }
  function todayISO() {
    var d = new Date();
    var mm = String(d.getMonth() + 1), dd = String(d.getDate());
    return d.getFullYear() + "-" + (mm.length < 2 ? "0" + mm : mm) + "-" + (dd.length < 2 ? "0" + dd : dd);
  }

  var opts = document.getElementById("opts");
  var addBtn = document.getElementById("add-date");
  if (opts && addBtn) {
    var defaultDay = opts.getAttribute("data-default-day");
    if (defaultDay) {
      var firstDay = opts.querySelector('input[name="day"]');
      if (firstDay && firstDay.value === defaultDay) {
        var t = todayISO();
        if (t !== defaultDay) firstDay.value = t;
      }
    }

    function nextDay() {
      var days = opts.querySelectorAll('input[name="day"]');
      for (var i = days.length - 1; i >= 0; i--) {
        var next = addDays(days[i].value, 1);
        if (next) return next;
      }
      return todayISO();
    }

    addBtn.addEventListener("click", function () {
      var rows = opts.querySelectorAll(".opt-row");
      var row = rows[rows.length - 1].cloneNode(true);
      var day = nextDay();
      row.querySelectorAll("input").forEach(function (f) { if (f.name === "day") f.value = day; });
      opts.appendChild(row);
      var d = row.querySelector('input[name="day"]');
      if (d) d.focus();
    });

    opts.addEventListener("click", function (e) {
      var btn = e.target.closest('[data-act="drop"]');
      if (!btn) return;
      var rows = opts.querySelectorAll(".opt-row");
      if (rows.length <= 1) {
        rows[0].querySelectorAll("input").forEach(function (f) { if (f.name === "day") f.value = ""; });
        return;
      }
      btn.closest(".opt-row").remove();
    });
  }
})();
