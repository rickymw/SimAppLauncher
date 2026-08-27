"use strict";

/* MotorHome web interface.
 *
 * No framework and no build step: the whole page is served from a go:embed
 * directory, so anything that needed compiling would mean a toolchain the repo
 * does not otherwise have. Every panel follows the same shape — fetch JSON,
 * build DOM nodes, replace a container's contents.
 *
 * Nothing here uses innerHTML with server data. Track names, driver names and
 * config paths are free text from iRacing and from the user's own files, and
 * building the DOM through createElement/textContent means a track called
 * "<script>" is a track called "<script>" rather than a bug. */

/* ── helpers ───────────────────────────────────────────────────────── */

const $ = (sel) => document.querySelector(sel);

function el(tag, attrs = {}, ...kids) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else if (v === true) node.setAttribute(k, "");
    else node.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    node.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return node;
}

function clear(node, ...kids) {
  node.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false));
}

let toastTimer = null;
function toast(msg, kind = "") {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "toast " + kind;
  t.hidden = false;
  clearTimeout(toastTimer);
  // Errors stay up longer: they usually carry a sentence worth reading, where a
  // success is just confirmation of something the user just watched happen.
  toastTimer = setTimeout(() => { t.hidden = true; }, kind === "error" ? 9000 : 3500);
}

/* api unwraps the {error} body every failing endpoint returns, so callers get a
 * readable message instead of "500". */
async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: opts.body ? { "Content-Type": "application/json" } : undefined,
    ...opts,
  });
  const text = await res.text();
  let body = null;
  if (text) {
    try { body = JSON.parse(text); } catch { /* non-JSON error page */ }
  }
  if (!res.ok) {
    const msg = (body && body.error) || text || `request failed (${res.status})`;
    const err = new Error(msg);
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return body;
}

/* withBusy disables a button for the length of an operation. Start and camera
 * restart both legitimately take tens of seconds, and without this the obvious
 * response to an unresponsive button is to press it again. */
async function withBusy(btn, fn) {
  const label = btn.textContent;
  btn.disabled = true;
  btn.textContent = "Working…";
  try {
    return await fn();
  } finally {
    btn.disabled = false;
    btn.textContent = label;
  }
}

const fmt = {
  secs: (s) => (s === null || s === undefined ? "—" : s.toFixed(3) + "s"),
  signed: (s, digits = 3) =>
    s === null || s === undefined ? "—" : (s > 0 ? "+" : "") + s.toFixed(digits),
  n: (v, digits = 1) => (v === null || v === undefined ? "—" : Number(v).toFixed(digits)),
  bytes: (b) => (b > 1 << 20 ? (b / (1 << 20)).toFixed(1) + " MB" : Math.round(b / 1024) + " KB"),
  when: (iso) => {
    if (!iso) return "—";
    const d = new Date(iso);
    return isNaN(d) ? iso : d.toLocaleString();
  },
};

/* table builds a <table> from column definitions and rows. Columns are
 * {head, get, num} — num right-aligns and uses the mono font. */
function table(cols, rows) {
  const thead = el("thead", {}, el("tr", {}, cols.map((c) =>
    el("th", { class: c.num ? "num" : null, text: c.head }))));
  const tbody = el("tbody", {}, rows.map((r) =>
    el("tr", {}, cols.map((c) => {
      const v = c.get(r);
      const td = el("td", { class: [c.num ? "num" : null, c.cls ? c.cls(r) : null].filter(Boolean).join(" ") || null });
      td.append(v instanceof Node ? v : document.createTextNode(v === null || v === undefined ? "—" : String(v)));
      return td;
    }))));
  return el("div", { class: "scroll-x" }, el("table", {}, thead, tbody));
}

function card(title, ...body) {
  return el("div", { class: "card" },
    el("div", { class: "card-head" }, el("h2", { text: title })),
    ...body);
}

/* ── tabs ──────────────────────────────────────────────────────────── */

const panels = {};
document.querySelectorAll(".panel").forEach((p) => { panels[p.id.replace("panel-", "")] = p; });

$("#tabs").addEventListener("click", (ev) => {
  const tab = ev.target.closest(".tab");
  if (!tab) return;
  selectPanel(tab.dataset.panel);
});

let currentPanel = "rig";
function selectPanel(name) {
  currentPanel = name;
  document.querySelectorAll(".tab").forEach((t) =>
    t.setAttribute("aria-selected", String(t.dataset.panel === name)));
  for (const [key, node] of Object.entries(panels)) node.hidden = key !== name;

  // The live stream is the one panel that costs something while hidden — it
  // reads shared memory every tick — so leaving it stops it rather than letting
  // it run behind another tab.
  if (name !== "live") stopLiveStream();

  if (name === "sessions") loadSessions();
  if (name === "pb") loadPB();
  if (name === "settings") loadSettings();
}

/* ── rig: apps ─────────────────────────────────────────────────────── */

function renderStatus(data) {
  $("#rig-summary").textContent = `${data.running}/${data.total} running`;
  if (!data.apps.length) {
    clear($("#rig-status"), el("p", { class: "muted", text: "No apps configured — add some in Settings." }));
    return;
  }
  clear($("#rig-status"), table([
    { head: "App", get: (a) => a.name },
    {
      head: "State",
      get: (a) => {
        if (a.outcome === "failed") return el("span", { class: "pill err", text: "ERROR" });
        const up = ["running", "launched", "already-running"].includes(a.outcome);
        return el("span", { class: "pill " + (up ? "on" : "off"), text: up ? "RUNNING" : "STOPPED" });
      },
    },
    { head: "PID", num: true, get: (a) => a.pid || "—" },
    { head: "Detail", get: (a) => a.error || a.process },
  ], data.apps));
}

async function refreshStatus() {
  try {
    renderStatus(await api("/api/status"));
  } catch (e) {
    clear($("#rig-status"), el("p", { class: "bad", text: e.message }));
  }
}

$("#btn-refresh-status").addEventListener("click", refreshStatus);

$("#btn-start").addEventListener("click", (ev) => withBusy(ev.target, async () => {
  try {
    const data = await api("/api/start", { method: "POST" });
    renderStatus(data);
    toast(`${data.running}/${data.total} apps running.`, "ok");
  } catch (e) { toast(e.message, "error"); }
}));

$("#btn-stop").addEventListener("click", (ev) => withBusy(ev.target, async () => {
  try {
    const data = await api("/api/stop", { method: "POST" });
    renderStatus(data);
    toast(data.running === 0 ? "All apps stopped." : `${data.running} still running.`,
      data.running === 0 ? "ok" : "error");
  } catch (e) { toast(e.message, "error"); }
}));

/* ── rig: usb ──────────────────────────────────────────────────────── */

function renderUSB(data) {
  const rows = data.devices || [];
  $("#usb-all-actions").hidden = rows.length === 0;

  if (!rows.length) {
    clear($("#usb-list"), el("p", { class: "muted", text: "No known devices." }));
    return;
  }

  clear($("#usb-list"), table([
    { head: "Device", get: (d) => d.name },
    { head: "Alias", get: (d) => el("code", { text: d.alias }) },
    {
      head: "State",
      get: (d) => el("span", {
        class: "pill " + (d.state === "enabled" ? "on" : d.state === "disabled" ? "err" : "off"),
        text: d.state,
      }),
    },
    {
      head: "",
      get: (d) => d.actionable
        ? el("button", {
            class: "btn small",
            onclick: (ev) => setUSB(ev.target, "toggle", d.alias),
          }, d.state === "enabled" ? "Disable" : "Enable")
        : el("span", { class: "muted small", text: "unplugged" }),
    },
  ], rows));

  const out = $("#usb-output");
  if (data.output) {
    out.textContent = data.output;
    out.hidden = false;
  }
}

async function setUSB(btn, action, target) {
  await withBusy(btn, async () => {
    try {
      renderUSB(await api("/api/usb", {
        method: "POST",
        body: JSON.stringify({ action, target }),
      }));
      toast(`${target}: ${action}`, "ok");
    } catch (e) {
      // A 409 still carries the refreshed device list: the change partly
      // happened, so show the new states next to the error rather than leaving
      // the panel stale.
      if (e.body && e.body.devices) renderUSB(e.body);
      toast(e.message, "error");
    }
  });
}

document.querySelectorAll("[data-usb-all]").forEach((btn) =>
  btn.addEventListener("click", (ev) => setUSB(ev.target, ev.target.dataset.usbAll, "all")));

async function loadUSB() {
  try {
    renderUSB(await api("/api/usb"));
  } catch (e) {
    $("#usb-all-actions").hidden = true;
    clear($("#usb-list"), el("p", { class: "muted", text: e.message }));
  }
}

/* ── rig: camera ───────────────────────────────────────────────────── */

$("#btn-camera").addEventListener("click", (ev) => withBusy(ev.target, async () => {
  const out = $("#camera-output");
  out.hidden = false;
  out.textContent = "Restarting the camera pipeline — this can take up to 30s if an app is holding the device…";
  try {
    const data = await api("/api/camera", { method: "POST" });
    const lines = [...(data.progress || [])];
    for (const s of data.services || []) {
      lines.push(`  ${s.restarted ? "[+]" : "[=]"} ${s.name} … ${s.restarted ? "restarted" : "already stopped"}`);
    }
    lines.push(data.restarted === 0
      ? "\nCamera pipeline was not running — Windows will start it fresh on next use."
      : `\nDone. ${data.restarted}/${(data.services || []).length} services restarted.`);
    out.textContent = lines.join("\n");
    toast("Camera pipeline restarted.", "ok");
  } catch (e) {
    out.textContent = e.message;
    toast(e.message, "error");
  }
}));

/* ── live ──────────────────────────────────────────────────────────── */

let liveSource = null;

function stopLiveStream() {
  if (!liveSource) return;
  liveSource.close();
  liveSource = null;
  $("#btn-live-toggle").textContent = "Start streaming";
  $("#live-state").textContent = "idle";
}

function startLiveStream() {
  stopLiveStream();
  const hz = $("#live-hz").value;
  liveSource = new EventSource(`/api/live/stream?hz=${encodeURIComponent(hz)}`);
  liveSource.onmessage = (ev) => {
    try { renderLive(JSON.parse(ev.data)); } catch { /* a malformed frame is not worth tearing the stream down for */ }
  };
  liveSource.onerror = () => {
    // EventSource reconnects on its own; say so rather than implying the panel
    // has died.
    $("#live-state").textContent = "reconnecting…";
  };
  $("#btn-live-toggle").textContent = "Stop streaming";
  $("#live-state").textContent = `streaming at ${hz} Hz`;
}

$("#btn-live-toggle").addEventListener("click", () => {
  if (liveSource) stopLiveStream(); else startLiveStream();
});

$("#live-hz").addEventListener("change", () => { if (liveSource) startLiveStream(); });

function gapRow(label, gap) {
  if (!gap) {
    return el("div", { class: "gap-row" },
      el("div", { class: "who" }, el("span", { class: "muted", text: `${label}: nobody on track` })));
  }
  const lapNote = gap.lapsDelta
    ? ` (${gap.lapsDelta > 0 ? "+" : ""}${gap.lapsDelta} lap${Math.abs(gap.lapsDelta) === 1 ? "" : "s"})`
    : "";
  return el("div", { class: "gap-row" },
    el("div", { class: "who" },
      el("span", { class: "muted small", text: label + "  " }),
      el("strong", { text: gap.driverName || "?" }),
      el("span", { class: "muted small", text: gap.carNumber ? `  #${gap.carNumber}` : "" }),
      el("span", { class: "muted small", text: lapNote })),
    el("div", { class: "delta", text: fmt.signed(gap.timeSeconds) + "s" }));
}

function renderLive(d) {
  const view = $("#live-view");
  if (!d.connected) {
    // The diagnostic goes underneath in small text: it matters when something
    // is genuinely wrong and is noise the rest of the time.
    clear(view,
      el("p", { class: "muted", text: d.message || "iRacing is not running, or you are not on track." }),
      d.detail && el("p", { class: "muted small", text: d.detail }));
    return;
  }

  const pos = d.position ? `${d.position}${d.fieldSize ? "/" + d.fieldSize : ""}` : "?";
  const cls = d.classPosition && d.classSize ? `${d.classPosition}/${d.classSize}` : null;

  clear(view,
    el("p", { class: "muted small" }, `${d.track || "?"} — ${d.car || "?"}`),
    el("div", { class: "live-hero" },
      el("div", { class: "stat" }, el("div", { class: "label", text: "Position" }), el("div", { class: "value", text: pos })),
      cls && el("div", { class: "stat" }, el("div", { class: "label", text: "In class" }), el("div", { class: "value", text: cls })),
      el("div", { class: "stat" }, el("div", { class: "label", text: "Lap" }), el("div", { class: "value", text: d.lap || "?" })),
      el("div", { class: "stat" }, el("div", { class: "label", text: "Lap %" }), el("div", { class: "value", text: fmt.n((d.lapDistPct || 0) * 100) }))),
    el("div", { class: "progress-track" },
      el("div", { class: "progress-fill", style: `width:${Math.max(0, Math.min(1, d.lapDistPct || 0)) * 100}%` })),
    el("div", { style: "margin-top:14px" },
      gapRow("Ahead", d.ahead),
      gapRow("Behind", d.behind)));
}

/* ── sessions / analysis ───────────────────────────────────────────── */

async function loadSessions() {
  try {
    const data = await api("/api/sessions");
    const sel = $("#session-file");
    clear(sel, el("option", { value: "", text: "most recent" }));
    for (const s of data.sessions || []) {
      sel.append(el("option", { value: s.path },
        `${s.index}. ${s.name} — ${fmt.when(s.modified)} (${fmt.bytes(s.size)})`));
    }
    $("#session-dir").textContent = data.truncated
      ? `${data.dir} — showing the ${(data.sessions || []).length} most recent`
      : data.dir;
  } catch (e) {
    $("#session-dir").textContent = e.message;
  }
}

$("#btn-analyze").addEventListener("click", (ev) => withBusy(ev.target, async () => {
  const params = new URLSearchParams();
  const file = $("#session-file").value;
  const lap = $("#session-lap").value.trim();
  const fuel = $("#session-fuel").value.trim();
  if (file) params.set("file", file);
  if (lap) params.set("lap", lap);
  if (fuel) params.set("fuelLaps", fuel);
  if ($("#session-updatemap").checked) params.set("updateMap", "true");

  clear($("#analysis"), el("p", { class: "muted", text: "Analysing…" }));
  try {
    renderAnalysis(await api("/api/analyze?" + params.toString()));
  } catch (e) {
    clear($("#analysis"), el("div", { class: "card" }, el("p", { class: "bad", text: e.message })));
  }
}));

function renderAnalysis(a) {
  const out = [];

  out.push(card("Session",
    el("table", {}, el("tbody", {},
      [["File", a.file], ["Driver", a.driver], ["Car", a.car], ["Track", a.track],
       ["Date", a.sessionDate], ["Samples", a.samples ? `${a.samples} @ ${a.tickRateHz}Hz` : null]]
        .filter(([, v]) => v)
        .map(([k, v]) => el("tr", {}, el("th", { text: k }), el("td", { text: String(v) })))))));

  if (a.trackMap) {
    const m = a.trackMap;
    const bits = [
      m.segmentCount ? `${m.segmentCount} segments` : `${(m.segments || []).length} segments`,
      m.geoMethod, m.confidence && `geometry: ${m.confidence}`,
      m.lapsUsed && `${m.lapsUsed} laps / ${m.sessionsUsed} sessions`,
      m.matchScore !== null && m.matchScore !== undefined ? `match ${fmt.n(m.matchScore * 100)}%` : "match n/a",
    ].filter(Boolean);
    out.push(card("Track map", el("p", { class: "muted small", text: bits.join("  ·  ") })));
  }

  if (a.pb) {
    const beat = a.pb.deltaToBest < 0;
    out.push(card("Personal best",
      el("p", {},
        el("strong", { text: a.pb.lapTimeFormatted || fmt.secs(a.pb.lapTime) }),
        el("span", { class: "muted small", text: `  ${[a.pb.date, a.pb.weather].filter(Boolean).join(" · ")}` })),
      el("p", { class: beat ? "good" : "muted" },
        beat ? `New personal best by ${fmt.n(Math.abs(a.pb.deltaToBest), 3)}s`
             : `This session's best is ${fmt.n(a.pb.deltaToBest, 3)}s off the PB`)));
  }

  if (a.laps && a.laps.length) {
    out.push(card("Laps", table([
      { head: "Lap", num: true, get: (l) => l.number },
      { head: "Time", num: true, get: (l) => l.timeFormatted || fmt.secs(l.lapTime) },
      { head: "Kind", get: (l) => l.kind },
      { head: "Flags", get: (l) => [l.cut && "cut", l.partialStart && "partial", !l.complete && "incomplete"].filter(Boolean).join(", ") || "—" },
      { head: "Comparable", get: (l) => (l.comparable ? "yes" : "—") },
    ], a.laps)));
  }

  if (a.sectors && (a.sectors.perLap || []).length) {
    const n = a.sectors.startPct.length;
    const cols = [{ head: "Lap", num: true, get: (r) => r.lap }];
    for (let i = 0; i < n; i++) {
      cols.push({ head: `S${i + 1}`, num: true, get: (r) => (r.times[i] === null ? "—" : fmt.n(r.times[i], 3)) });
    }
    cols.push({ head: "Lap time", num: true, get: (r) => fmt.n(r.lapTime, 3) });
    const extra = [];
    if (a.sectors.best) {
      extra.push(el("p", { class: "muted small" },
        "Best sectors: " + a.sectors.best.map((t, i) =>
          `S${i + 1} ${fmt.n(t, 3)} (lap ${a.sectors.bestFromLap[i]})`).join("  ·  ")));
    }
    if (a.sectors.theoretical) {
      extra.push(el("p", { class: "muted small", text: `Theoretical best: ${fmt.n(a.sectors.theoretical, 3)}s` }));
    }
    out.push(card("Sectors", table(cols, a.sectors.perLap), ...extra));
  }

  const lap = a.analysedLap;
  if (lap) {
    if (lap.phases && lap.phases.length) {
      out.push(card(`Phases — lap ${lap.number} (${lap.timeFormatted || fmt.secs(lap.lapTime)}, ${lap.selection})`,
        table([
          { head: "Segment", get: (p) => p.segment },
          { head: "Phase", get: (p) => p.phase },
          { head: "Entry", num: true, get: (p) => fmt.n(p.speedEntryKph) },
          { head: "Exit", num: true, get: (p) => fmt.n(p.speedExitKph) },
          { head: "Peak", num: true, get: (p) => fmt.n(p.peakSpeedKph) },
          { head: "OnBrk%", num: true, get: (p) => fmt.n(p.brakePct) },
          { head: "PkBrk%", num: true, get: (p) => fmt.n(p.peakBrakePct) },
          { head: "Thr%", num: true, get: (p) => fmt.n(p.throttlePct) },
          { head: "LatG", num: true, get: (p) => fmt.n(p.latGAvg, 2) },
          { head: "Wheel°", num: true, get: (p) => fmt.n(p.peakSteerDeg, 0) },
          { head: "Corr", num: true, get: (p) => p.corrections },
          { head: "ABS", num: true, get: (p) => p.absSamples },
          { head: "Lock", num: true, get: (p) => p.lockupSamples },
          { head: "Spin", num: true, get: (p) => p.wheelspinSamples },
          { head: "Coast", num: true, get: (p) => fmt.n(p.coastSeconds, 2) },
        ], lap.phases)));
    }

    if (lap.vsPB && lap.vsPB.length) {
      // Faster/more-throttle is green, more braking or more error counts is red.
      const signed = (v, digits = 1, goodWhenPositive = true) => {
        const node = el("span", { text: fmt.signed(v, digits) });
        if (Math.abs(v) > 1e-6) node.className = (v > 0) === goodWhenPositive ? "good" : "bad";
        return node;
      };
      out.push(card("vs personal best", table([
        { head: "Segment", get: (d) => d.segment },
        { head: "Phase", get: (d) => d.phase },
        { head: "dEntry", num: true, get: (d) => signed(d.dSpeedEntryKph) },
        { head: "dExit", num: true, get: (d) => signed(d.dSpeedExitKph) },
        { head: "dOnBrk", num: true, get: (d) => signed(d.dBrakePct, 1, false) },
        { head: "dPkBrk", num: true, get: (d) => signed(d.dPeakBrakePct, 1, false) },
        { head: "dThr", num: true, get: (d) => signed(d.dThrottlePct) },
        { head: "dLatG", num: true, get: (d) => signed(d.dLatGAvg, 2) },
        { head: "dCorr", num: true, get: (d) => signed(d.dCorrections, 0, false) },
        { head: "dABS", num: true, get: (d) => signed(d.dAbsSamples, 0, false) },
        { head: "dLock", num: true, get: (d) => signed(d.dLockupSamples, 0, false) },
        { head: "dSpin", num: true, get: (d) => signed(d.dWheelspinSamples, 0, false) },
        { head: "dCoast", num: true, get: (d) => signed(d.dCoastSeconds, 2, false) },
      ], lap.vsPB)));
    }

    if (lap.exitImpact && lap.exitImpact.length) {
      out.push(card("Corner exit → straight peak", table([
        { head: "Corner", get: (r) => r.corner },
        { head: "Exit speed", num: true, get: (r) => fmt.n(r.cornerExitSpeedKph) },
        { head: "Straight", get: (r) => r.straight },
        { head: "Peak speed", num: true, get: (r) => fmt.n(r.straightPeakSpeedKph) },
      ], lap.exitImpact)));
    }

    if (lap.tyres && lap.tyres.corners) {
      const rows = Object.entries(lap.tyres.corners).map(([k, v]) => ({ corner: k, ...v }));
      out.push(card("Tyres", table([
        { head: "Corner", get: (t) => t.corner },
        { head: "Temp I / M / O", num: true, get: (t) => `${fmt.n(t.tempInnerC, 0)} / ${fmt.n(t.tempMidC, 0)} / ${fmt.n(t.tempOuterC, 0)}` },
        { head: "Wear I / M / O", num: true, get: (t) => `${fmt.n(t.wornInnerPct, 0)} / ${fmt.n(t.wornMidPct, 0)} / ${fmt.n(t.wornOuterPct, 0)}` },
        { head: "Pressure kPa", num: true, get: (t) => fmt.n(t.pressureKpa) },
      ], rows),
      el("p", { class: "muted small", text: `Brake bias: ${fmt.n(lap.tyres.brakeBias, 2)}` })));
    }
  }

  if (a.consistency && a.consistency.length) {
    out.push(card("Consistency", table([
      { head: "Segment", get: (c) => c.segment },
      { head: "Phase", get: (c) => c.phase },
      { head: "Laps", num: true, get: (c) => c.laps },
      { head: "Entry", num: true, get: (c) => `${fmt.n(c.entrySpeedMeanKph)} ± ${fmt.n(c.entrySpeedSdKph)}` },
      { head: "Exit", num: true, get: (c) => `${fmt.n(c.exitSpeedMeanKph)} ± ${fmt.n(c.exitSpeedSdKph)}` },
      { head: "PkBrk SD", num: true, get: (c) => fmt.n(c.peakBrakeSdPct) },
      { head: "LatG SD", num: true, get: (c) => fmt.n(c.latGSd, 2) },
      { head: "Coast SD", num: true, get: (c) => fmt.n(c.coastSdSeconds, 2) },
      { head: "Best exit", num: true, get: (c) => `${fmt.n(c.bestExitSpeedKph)} (lap ${c.bestExitLap})` },
    ], a.consistency)));
  }

  if (a.fuel) {
    const f = a.fuel;
    out.push(card("Fuel",
      el("table", {}, el("tbody", {},
        [["Start", `${fmt.n(f.startLitres, 2)} L`],
         ["End", `${fmt.n(f.endLitres, 2)} L`],
         ["Used", `${fmt.n(f.usedLitres, 2)} L over ${f.lapsMeasured} laps`],
         ["Per lap (avg)", `${fmt.n(f.avgPerLapLitres, 3)} L`],
         ["Per lap (worst)", `${fmt.n(f.worstPerLapLitres, 3)} L`],
         ["Laps remaining", `${fmt.n(f.lapsRemainingAvg)} avg  ·  ${fmt.n(f.lapsRemainingWorst)} worst-lap`],
         f.refuelled && ["Note", "A lap gained fuel; its consumption is excluded from the averages."]]
          .filter(Boolean)
          .map(([k, v]) => el("tr", {}, el("th", { text: k }), el("td", { text: String(v) })))))));
  }

  if (a.notes && a.notes.length) {
    out.push(card("Voice notes", table([
      { head: "Lap", num: true, get: (n) => (n.located ? n.lap : "—") },
      { head: "Where", get: (n) => (n.located ? n.segment : "—") },
      { head: "Lap %", num: true, get: (n) => (n.located ? fmt.n(n.lapDistPct * 100) : "—") },
      { head: "Note", get: (n) => n.text },
    ], a.notes)));
  }

  if (a.traces && a.traces.length) {
    for (const t of a.traces) {
      out.push(card(`Trace — ${t.SegmentName || t.segmentName || "segment"}`,
        el("pre", { class: "output", text: (t.Rows || t.rows || []).join("\n") })));
    }
  }

  clear($("#analysis"), out);
}

/* ── personal bests ────────────────────────────────────────────────── */

let pbEntries = [];

async function loadPB() {
  try {
    const data = await api("/api/pb");
    pbEntries = data.entries || [];
    $("#pb-summary").textContent = `${pbEntries.length} stored`;
    renderPBList();
  } catch (e) {
    clear($("#pb-list"), el("p", { class: "bad", text: e.message }));
  }
}

function renderPBList() {
  const q = $("#pb-filter").value.trim().toLowerCase();
  const rows = pbEntries.filter((e) =>
    !q || (e.car + " " + e.track).toLowerCase().includes(q));

  if (!rows.length) {
    clear($("#pb-list"), el("p", { class: "muted", text: pbEntries.length ? "Nothing matches that filter." : "No personal bests stored yet." }));
    return;
  }

  clear($("#pb-list"), table([
    { head: "Track", get: (e) => e.track },
    { head: "Car", get: (e) => e.car },
    { head: "Lap", num: true, get: (e) => e.lapTimeFormatted || fmt.secs(e.lapTime) },
    { head: "Date", get: (e) => e.date || "—" },
    {
      head: "Carries",
      get: (e) => [e.hasPhases && "phases", e.hasSetup && "setup",
                   e.brakePointCount && `${e.brakePointCount} brake points`].filter(Boolean).join(", ") || "—",
    },
    {
      head: "",
      get: (e) => el("button", { class: "btn small", onclick: () => showPBDetail(e.key) }, "Open"),
    },
  ], rows));
}

$("#pb-filter").addEventListener("input", renderPBList);
$("#btn-pb-refresh").addEventListener("click", loadPB);

async function showPBDetail(key) {
  const target = $("#pb-detail");
  clear(target, el("div", { class: "card" }, el("p", { class: "muted", text: "Loading…" })));
  try {
    const data = await api("/api/pb?key=" + encodeURIComponent(key));
    const e = (data.entries || [])[0];
    if (!e) { clear(target); return; }

    const out = [card(`${e.track} — ${e.car}`,
      el("p", {},
        el("strong", { text: e.lapTimeFormatted || fmt.secs(e.lapTime) }),
        el("span", { class: "muted small", text: `  ${[e.date, e.weather].filter(Boolean).join(" · ")}` })))];

    if (e.setup && e.setup.length) {
      out.push(card("Setup", table([
        { head: "Field", get: (f) => f.path },
        { head: "Value", get: (f) => f.value },
      ], e.setup)));
    }

    if (e.brakeEntries && e.brakeEntries.length) {
      out.push(card("Brake points", table([
        { head: "Segment", get: (b) => b.segment },
        { head: "Lap %", num: true, get: (b) => fmt.n(b.pct * 100, 2) },
        { head: "Laps used", num: true, get: (b) => b.lapsUsed },
      ], e.brakeEntries)));
    }

    if (e.phases && e.phases.length) {
      out.push(card("Phases", table([
        { head: "Segment", get: (p) => p.segName },
        { head: "Phase", get: (p) => p.kind },
        { head: "Entry", num: true, get: (p) => fmt.n(p.speedEntryKPH) },
        { head: "Exit", num: true, get: (p) => fmt.n(p.speedExitKPH) },
        { head: "OnBrk%", num: true, get: (p) => fmt.n(p.brakePct) },
        { head: "PkBrk%", num: true, get: (p) => fmt.n(p.peakBrakePct) },
        { head: "Thr%", num: true, get: (p) => fmt.n(p.throttlePct) },
        { head: "LatG", num: true, get: (p) => fmt.n(p.latGAvg, 2) },
        { head: "Corr", num: true, get: (p) => p.corrections },
        { head: "ABS", num: true, get: (p) => p.absCount },
        { head: "Lock", num: true, get: (p) => p.lockupSamples },
        { head: "Spin", num: true, get: (p) => p.wheelspinSamples },
      ], e.phases)));
    }

    clear(target, out);
    target.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (err) {
    clear(target, el("div", { class: "card" }, el("p", { class: "bad", text: err.message })));
  }
}

/* ── settings ──────────────────────────────────────────────────────── */

let settings = null;

async function loadSettings() {
  try {
    settings = await api("/api/config");
    $("#settings-path").textContent = settings.path;
    renderSettings();
  } catch (e) {
    // A config that will not load is exactly when this panel is most needed —
    // it is the only screen that can repair one — so offer a blank document to
    // edit rather than a dead end. The server is dispatched ahead of the config
    // load for the same reason.
    settings = null;
    clear($("#settings-form"),
      el("p", { class: "bad", text: e.message }),
      el("p", { class: "muted small", text: "Nothing has been overwritten. You can start from a blank config and save over the broken one." }),
      el("div", { class: "actions" },
        el("button", {
          class: "btn",
          onclick: () => {
            settings = {
              path: "",
              windowStyles: ["Normal", "Hidden"],
              config: { driver: "", ibtDir: "", hotkey: "", whisperPath: "", whisperModel: "", apps: [] },
            };
            renderSettings();
          },
        }, "Start from a blank config")));
  }
}

function textField(label, value, onInput, hint) {
  return el("div", { class: "field" },
    el("label", { text: label }),
    el("div", {},
      el("input", { type: "text", value: value ?? "", oninput: (ev) => onInput(ev.target.value) }),
      hint && el("div", { class: "muted small", text: hint })));
}

function renderSettings() {
  const c = settings.config;
  const form = $("#settings-form");

  const general = el("div", {},
    textField("Driver", c.driver, (v) => { c.driver = v; },
      "iRacing UserName — used to pick your car out of a multi-class session."),
    textField("iBT directory", c.ibtDir, (v) => { c.ibtDir = v; },
      "Where analyze looks for .ibt telemetry files."),
    textField("Voice note hotkey", c.hotkey, (v) => { c.hotkey = v; },
      "Set this with `motorhome notes set-hotkey` — it captures the real key code."),
    textField("whisper-cli path", c.whisperPath, (v) => { c.whisperPath = v; }),
    textField("whisper model path", c.whisperModel, (v) => { c.whisperModel = v; }));

  const appsBox = el("div", {});
  const drawApps = () => {
    clear(appsBox, (c.apps || []).map((app, i) => el("div", { class: "app-row" },
      el("div", { class: "app-head" },
        el("strong", { text: app.name || `App ${i + 1}` }),
        el("span", {},
          el("button", { class: "btn small", onclick: () => { moveApp(i, -1); }, disabled: i === 0 }, "↑"),
          " ",
          el("button", { class: "btn small", onclick: () => { moveApp(i, 1); }, disabled: i === (c.apps.length - 1) }, "↓"),
          " ",
          el("button", { class: "btn small danger", onclick: () => { c.apps.splice(i, 1); drawApps(); } }, "Remove"))),
      textField("Name", app.name, (v) => { app.name = v; }),
      textField("Path", app.path, (v) => { app.path = v; }),
      textField("Arguments", app.args, (v) => { app.args = v; },
        "One string, split on spaces — not a list."),
      textField("Process name", app.processName, (v) => { app.processName = v; },
        "Exe stem as Task Manager shows it; falls back to Name. Trailing spaces will silently stop it matching."),
      el("div", { class: "field" },
        el("label", { text: "Window style" }),
        el("select", { onchange: (ev) => { app.windowStyle = ev.target.value; } },
          ["", ...settings.windowStyles].map((s) =>
            el("option", { value: s, selected: (app.windowStyle || "") === s }, s || "(default)")))),
      el("div", { class: "field" },
        el("label", { text: "Delay after (ms)" }),
        el("input", {
          type: "number", min: "0", value: app.delayMs ?? 0,
          oninput: (ev) => { app.delayMs = Number(ev.target.value) || 0; },
        })),
      el("div", { class: "field" },
        el("label", { text: "Elevate" }),
        el("input", {
          type: "checkbox", checked: !!app.elevate,
          onchange: (ev) => { app.elevate = ev.target.checked; },
        })))));
  };
  const moveApp = (i, delta) => {
    const j = i + delta;
    if (j < 0 || j >= c.apps.length) return;
    [c.apps[i], c.apps[j]] = [c.apps[j], c.apps[i]];
    drawApps();
  };
  drawApps();

  clear(form,
    card("General", general),
    card("Apps",
      el("p", { class: "muted small", text: "Started in this order; each app's delay is waited out before the next." }),
      appsBox,
      el("div", { class: "actions" },
        el("button", {
          class: "btn",
          onclick: () => {
            c.apps = c.apps || [];
            c.apps.push({ name: "", path: "", args: "", windowStyle: "", delayMs: 0, elevate: false, processName: "" });
            drawApps();
          },
        }, "Add app"))));
}

$("#btn-reload-settings").addEventListener("click", loadSettings);

$("#btn-save-settings").addEventListener("click", (ev) => withBusy(ev.target, async () => {
  if (!settings) return;
  try {
    settings = await api("/api/config", { method: "PUT", body: JSON.stringify(settings.config) });
    renderSettings();
    toast("Settings saved.", "ok");
    // The app list may have changed, so the rig panel's table is now stale.
    refreshStatus();
  } catch (e) {
    toast(e.message, "error");
  }
}));

/* ── boot ──────────────────────────────────────────────────────────── */

refreshStatus();
loadUSB();
