// Server vykreslí tabulku i výsledky. Tenhle skript jen zpříjemňuje
// klikání: bez něj jsou výsledky pořád čitelné, jen se nedá hlasovat.
(function () {
  "use strict";

  var VALS = ["no", "yes", "maybe"];

  var GLYPH = {
    yes: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 6.4 4.6 9 10 3"/></svg>',
    maybe: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M1.5 7.2c1-2.2 2.2-2.2 3.2 0s2.2 2.2 3.2 0"/></svg>',
    no: '<svg class="glyph" viewBox="0 0 12 12" fill="none" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3.5 6h5"/></svg>'
  };

  var toastEl = document.getElementById("toast");
  var toastTimer;

  function toast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.classList.add("on");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toastEl.classList.remove("on"); }, 1900);
  }

  // Hláška po uložení zmizí sama, ať nezůstane viset přes obsah.
  if (toastEl && toastEl.classList.contains("on")) {
    toastTimer = setTimeout(function () { toastEl.classList.remove("on"); }, 2600);
  }

  /* ---------- tabulka ---------- */

  var form = document.getElementById("vote-form");

  if (form) {
    form.addEventListener("click", function (e) {
      var cell = e.target.closest(".cell.live");
      if (!cell) return;

      var id = cell.getAttribute("data-opt");
      var input = document.getElementById("in-" + id);
      var next = VALS[(VALS.indexOf(cell.getAttribute("data-v")) + 1) % VALS.length];

      cell.setAttribute("data-v", next);
      cell.innerHTML = GLYPH[next];
      if (input) input.value = next;

      // Popisek pro odečítač obrazovky musí jít s hodnotou.
      var label = cell.getAttribute("aria-label");
      if (label) {
        var head = label.split(":")[0];
        var word = form.getAttribute("data-label-" + next) || next;
        cell.setAttribute("aria-label", head + ": " + word + ". " + (form.getAttribute("data-hint") || ""));
      }
      updateMarked();
    });

    var marked = document.getElementById("marked");
    var markedTpl = marked ? marked.getAttribute("data-tpl") : null;

    function updateMarked() {
      if (!marked || !markedTpl) return;
      var n = 0;
      form.querySelectorAll(".cell.live").forEach(function (c) {
        if (c.getAttribute("data-v") !== "no") n++;
      });
      marked.textContent = markedTpl.replace("%d", n);
    }
    updateMarked();

    // Jméno se propisuje do hlavičky sloupce, aby bylo vidět, čí je.
    var nick = document.getElementById("nick");
    var myName = document.getElementById("my-name");
    if (nick && myName) {
      var fallback = myName.textContent;
      nick.addEventListener("input", function () {
        var v = nick.value.trim();
        myName.textContent = v || fallback;
        myName.setAttribute("title", v);
      });
    }
  }

  /* ---------- odkaz ---------- */

  var copy = document.getElementById("copy-link");
  if (copy) {
    copy.addEventListener("click", function () {
      var url = location.origin + location.pathname;
      var ok = copy.getAttribute("data-ok");
      var fail = copy.getAttribute("data-fail");
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(url).then(
          function () { toast(ok); },
          function () { toast(fail); }
        );
      } else {
        toast(fail);
      }
    });
  }

  /* ---------- zakládání ---------- */

  var opts = document.getElementById("opts");
  var addBtn = document.getElementById("add-date");

  // Datum je "YYYY-MM-DD". Počítá se v UTC, aby posun časové zóny
  // nepřehodil den; přetáčení měsíce, roku i přestupného února řeší Date.
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
    var mm = String(d.getMonth() + 1);
    var dd = String(d.getDate());
    return d.getFullYear() + "-" + (mm.length < 2 ? "0" + mm : mm) + "-" + (dd.length < 2 ? "0" + dd : dd);
  }

  if (opts && addBtn) {
    // Server zná dnešek jen podle svého TZ. Dokud do pole nikdo nesáhl,
    // srovnáme ho na dnešek podle prohlížeče.
    var defaultDay = opts.getAttribute("data-default-day");
    if (defaultDay) {
      var firstDay = opts.querySelector('input[name="day"]');
      if (firstDay && firstDay.value === defaultDay) {
        var t = todayISO();
        if (t !== defaultDay) firstDay.value = t;
      }
    }

    // Nový termín = den po posledním zadaném. Když je poslední řádek
    // prázdný, bereme poslední vyplněný nad ním; když není žádný, dnešek.
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
      var last = rows[rows.length - 1];
      var row = last.cloneNode(true);
      var day = nextDay();

      row.querySelectorAll("input").forEach(function (f) {
        if (f.name === "day") f.value = day;
        // Čas se nechá podle posledního řádku, obvykle sedí.
      });
      opts.appendChild(row);
      var d = row.querySelector('input[name="day"]');
      if (d) d.focus();
    });

    opts.addEventListener("click", function (e) {
      var btn = e.target.closest('[data-act="drop"]');
      if (!btn) return;
      var rows = opts.querySelectorAll(".opt-row");
      if (rows.length <= 1) {
        // Poslední řádek se nemaže, jen vyprázdní.
        rows[0].querySelectorAll("input").forEach(function (f) {
          if (f.name === "day") f.value = "";
        });
        return;
      }
      btn.closest(".opt-row").remove();
    });
  }
})();
