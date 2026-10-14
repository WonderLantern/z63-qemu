import { Channel } from "mygo-runtime";
import { Camera, events, type LogBatch, type Status } from "./mygo";
import { buildBack, buildFront, buildTop, runMonitor, runPanel } from "./camera";
import { hold, release, releaseAll } from "./keys";
import { LogView, Terminal } from "./logview";
import { attachTouch, onTouchError } from "./touch";
import { $, esc, matcher, store, toast } from "./util";

// ---- the body, each picture scaled to its space ----

const canvas = buildBack($("#body"));
const panelLcd = buildTop($("#top"));
buildFront($("#front"));
const fit = () => {
  const wrap = $("#bodyWrap");
  const body = $("#body");
  const s = Math.min((wrap.clientWidth - 32) / body.offsetWidth, (wrap.clientHeight - 32) / body.offsetHeight);
  body.style.transform = `translate(-50%, -50%) scale(${Math.max(s, 0.2)})`;
};
const fitTop = () => {
  const wrap = $("#topWrap");
  const t = $("#top");
  const s = (wrap.clientWidth - 24) / t.offsetWidth;
  t.style.transform = `scale(${s})`;
  wrap.style.height = `${t.offsetHeight * s + 16}px`;
};
new ResizeObserver(fit).observe($("#bodyWrap"));
new ResizeObserver(fitTop).observe($("#topWrap"));

// The monitor at 1:1, a touch screen too; clicks on the monitor are
// touches, so it opens from the header.
const zoom = $("#zoom");
const zoomLcd = $<HTMLCanvasElement>("#zoomLcd");
const stats = runMonitor(canvas, zoomLcd, () => !zoom.hidden);
const panelStats = runPanel(panelLcd);
attachTouch(zoomLcd);
$("#zoomBtn").addEventListener("click", () => {
  zoomLcd.width = canvas.width;
  zoomLcd.height = canvas.height;
  zoomLcd.getContext("2d")!.drawImage(canvas, 0, 0);
  zoom.hidden = false;
});
$("#zoomClose").addEventListener("click", () => (zoom.hidden = true));
zoom.addEventListener("click", (e) => {
  if (e.target === zoom) zoom.hidden = true;
});
onTouchError((msg) =>
  toast(
    /Input handler not found/.test(msg)
      ? "Touch: no touch panel is modelled in the machine yet -- QEMU has no handler for multi-touch input"
      : "Touch: " + msg.replace(/^Error: /, ""),
    "error",
  ),
);

// ---- state ----

let status: Status = { state: "off" } as Status;

function applyStatus(s: Status) {
  status = s;
  const pill = $("#pill");
  pill.className = "pill " + s.state;
  $("#state").textContent = s.state;
  $("#run").textContent = s.run || "";
  $("#detail").textContent = s.detail || "";
  $("#detail").title = s.detail || "";
  const on = s.state !== "off" && s.state !== "exited";
  $("#power").textContent = on ? "Power off" : "Power on";
  $("#power").classList.toggle("primary", !on);
  $<HTMLButtonElement>("#restart").disabled = !on;
  $<HTMLButtonElement>("#pause").disabled = !(s.state === "running" || s.state === "paused" || s.state === "stopped");
  $("#pause").textContent = s.state === "running" ? "Pause" : "Continue";
  document.body.classList.toggle("off", !on);
  renderRunInfo();
}
events.status.on(applyStatus);
Camera.status().then(applyStatus);

const act = (fn: () => Promise<unknown>) => () => fn().catch((e) => toast(String(e), "error"));
$("#power").addEventListener(
  "click",
  act(() => (status.state === "off" || status.state === "exited" ? Camera.start() : Camera.stop())),
);
$("#restart").addEventListener("click", act(() => Camera.restart()));
$("#pause").addEventListener("click", act(() => (status.state === "running" ? Camera.pause() : Camera.resume())));
$("#shot").addEventListener(
  "click",
  act(async () => toast("saved " + (await Camera.screenshot()))),
);
const setDock = (open: boolean) => {
  document.body.classList.toggle("dock-closed", !open);
  $("#dockToggle").classList.toggle("on", open);
  store.set("dock", open ? "1" : "0");
  requestAnimationFrame(fit);
};
setDock(store.get("dock") !== "0");
$("#dockToggle").addEventListener("click", () => setDock(document.body.classList.contains("dock-closed")));

// Frames shown and composed per second, from the monitor's counters.
let prev = { shown: 0, composed: 0, t: performance.now() };
setInterval(() => {
  const t = performance.now();
  const dt = (t - prev.t) / 1000;
  const shown = (stats.shown - prev.shown) / dt;
  const composed = (stats.composed - prev.composed) / dt;
  prev = { shown: stats.shown, composed: stats.composed, t };
  $("#fps").textContent =
    status.state === "off" ? "" : `${stats.w ? stats.w + "×" + stats.h : "no frame"} · ${composed.toFixed(0)} composed/s · ${shown.toFixed(0)} new/s`;
  $("#topHint").textContent =
    `control panel · ${panelStats.packets ? panelStats.packets + " packets from the body MCU" : "drawn by the body MCU"} · click it to enlarge`;
}, 1000);

// ---- the keyboard: camera buttons while no text field has focus ----

const keymap: Record<string, string> = {
  ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right",
  Enter: "ok", m: "menu", p: "play", Backspace: "delete", Delete: "delete",
  h: "half", " ": "full", d: "disp", i: "i", a: "af-on", r: "movie", o: "release-mode",
  "+": "zoom-in", "=": "zoom-in", "-": "zoom-out", F1: "fn", F2: "fn2",
};
const typing = () => {
  const a = document.activeElement;
  return a instanceof HTMLInputElement || a instanceof HTMLTextAreaElement;
};
const down = new Set<string>();
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !zoom.hidden) {
    zoom.hidden = true;
    return;
  }
  if (typing() || e.metaKey || e.ctrlKey || e.altKey) return;
  const k = keymap[e.key.length === 1 ? e.key.toLowerCase() : e.key];
  if (!k) return;
  e.preventDefault();
  if (down.has(e.code)) return; // auto-repeat
  down.add(e.code);
  hold(k);
});
document.addEventListener("keyup", (e) => {
  if (!down.delete(e.code)) return;
  const k = keymap[e.key.length === 1 ? e.key.toLowerCase() : e.key];
  if (k) release(k);
});
window.addEventListener("blur", () => {
  down.clear();
  releaseAll();
});

// ---- the Linux console ----

const kernelRe = /^\[\s*\d+\.\d+\]/;
const conView = new LogView($("#conLog"), {
  cap: 20000,
  show: 3000,
  follow: $<HTMLButtonElement>("#conFollow"),
  fmt: (l) => {
    const m = kernelRe.exec(l);
    if (m) return `<span class="ts">${m[0]}</span>${esc(l.slice(m[0].length))}`;
    if (l.startsWith("── ")) return `<span class="sep">${esc(l)}</span>`;
    return `<span class="rtos">${esc(l)}</span>`;
  },
});
const term = new Terminal(conView);
let conMode = "all";
for (const b of document.querySelectorAll<HTMLButtonElement>("#conFilter button")) {
  b.addEventListener("click", () => {
    conMode = b.dataset.f!;
    document.querySelectorAll("#conFilter button").forEach((x) => x.classList.toggle("on", x === b));
    conView.filter =
      conMode === "all" ? null : conMode === "kernel" ? (l) => kernelRe.test(l) : (l) => !kernelRe.test(l);
    conView.mark(true);
  });
}
$("#conClear").addEventListener("click", () => conView.clear());
Camera.serial(
  new Channel<LogBatch>((b) => {
    if (b.reset) {
      conView.add([`── ${b.run} ──`]);
    }
    if (b.dropped) conView.add([`── ${b.dropped} chunks not shown ──`]);
    term.write(b.items.join(""));
  }),
);
$("#conForm").addEventListener("submit", (e) => {
  e.preventDefault();
  const inp = $<HTMLInputElement>("#conInput");
  Camera.serialInput(inp.value + "\n").catch((err) => toast(String(err), "error"));
  inp.value = "";
});
$("#conCtrlC").addEventListener("click", () => Camera.serialInput("\x03").catch((err) => toast(String(err), "error")));

// ---- the dock ----

let tab = store.get("tab") || "stderr";
const dockOpen = () => !document.body.classList.contains("dock-closed");
const showTab = (t: string) => {
  tab = t;
  store.set("tab", t);
  for (const x of document.querySelectorAll<HTMLElement>("#tabs button, .tab")) x.classList.toggle("on", x.dataset.tab === t);
  errView.mark(true);
  fwView.mark(true);
  if (t === "stderr") refreshTags();
};
for (const b of document.querySelectorAll<HTMLButtonElement>("#tabs button")) b.addEventListener("click", () => showTab(b.dataset.tab!));

// QEMU stderr, with lines muted by their tag.
const tagRe = /^(?:qemu-system-aarch64: )?([A-Z][A-Z0-9_]{2,})\b/;
const tagOf = (l: string) => tagRe.exec(l)?.[1] ?? "(other)";
const muted = new Set<string>(JSON.parse(store.get("muted") || "[]"));
let errMatch: ((s: string) => boolean) | null = null;
const errView = new LogView($("#errLog"), {
  cap: 30000,
  show: 2500,
  follow: $<HTMLButtonElement>("#errFollow"),
  visible: () => dockOpen() && tab === "stderr",
  fmt: (l) => {
    const s = l.startsWith("qemu-system-aarch64: ") ? l.slice(21) : l;
    const m = tagRe.exec(s);
    const cls = /stopping|error|refus|abort/i.test(s) ? "bad" : /warning|declared|stub/i.test(s) ? "warn" : "";
    const body = m ? `<span class="tag">${m[1]}</span>${esc(s.slice(m[0].length))}` : esc(s);
    return cls ? `<span class="${cls}">${body}</span>` : body;
  },
});
const errFilter = () => {
  errView.filter = (l) => !muted.has(tagOf(l)) && (!errMatch || errMatch(l));
  $("#errMuted").textContent = muted.size ? `muted: ${[...muted].join(", ")}` : "click a tag to mute it";
  errView.mark(true);
};
$<HTMLInputElement>("#errFilter").addEventListener("input", (e) => {
  errMatch = matcher((e.target as HTMLInputElement).value);
  errFilter();
});
errFilter();
let errLines = 0;
Camera.stderr(
  new Channel<LogBatch>((b) => {
    if (b.reset) {
      errLines = 0;
      errView.add([`z63ui: ── run ${b.run} ──`]);
    }
    if (b.dropped) errView.add([`z63ui: ── ${b.dropped} lines not shown (too fast) ──`]);
    errLines += b.items.length + b.dropped;
    $("#stderrCount").textContent = errLines.toLocaleString();
    errView.add(b.items);
  }),
);

async function refreshTags() {
  if (!dockOpen() || tab !== "stderr") return;
  const tags = await Camera.tags();
  $("#tagTable").innerHTML =
    `<tr><th>tag</th><th>lines</th></tr>` +
    tags
      .slice(0, 60)
      .map(
        (t) =>
          `<tr class="${muted.has(t.tag) ? "muted" : ""}" data-tag="${esc(t.tag)}"><td>${esc(t.tag)}</td><td>${t.count.toLocaleString()}</td></tr>`,
      )
      .join("");
}
setInterval(refreshTags, 2000);
$("#tagTable").addEventListener("click", (e) => {
  const tr = (e.target as HTMLElement).closest<HTMLElement>("tr[data-tag]");
  if (!tr) return;
  const t = tr.dataset.tag!;
  muted.has(t) ? muted.delete(t) : muted.add(t);
  store.set("muted", JSON.stringify([...muted]));
  tr.classList.toggle("muted", muted.has(t));
  errFilter();
});

// The firmwares' own logs (libz63log).
const fwView = new LogView($("#fwLog"), {
  cap: 30000,
  show: 2500,
  follow: $<HTMLButtonElement>("#fwFollow"),
  visible: () => dockOpen() && tab === "fw",
  // <seq> <host us> cpu<n> <log|syslog|mcu|error|call> ... (z63log.c)
  fmt: (l) => {
    const m = /^(\d+\s+\d+\s+cpu\d+\s+)(\S+)/.exec(l);
    const cls = m?.[2] === "error" ? "bad" : m?.[2] === "mcu" || m?.[2] === "syslog" ? "warn" : l.startsWith("#") ? "dim" : "";
    const body = m ? `<span class="ts">${esc(m[1]!)}</span><span class="tag">${esc(m[2]!)}</span>${esc(l.slice(m[0].length))}` : esc(l);
    return cls ? `<span class="${cls}">${body}</span>` : body;
  },
});
$<HTMLInputElement>("#fwFilter").addEventListener("input", (e) => {
  fwView.filter = matcher((e.target as HTMLInputElement).value);
  fwView.mark(true);
});
let fwLines = 0;
Camera.firmware(
  new Channel<LogBatch>((b) => {
    if (b.reset) {
      fwLines = 0;
      fwView.add([`── run ${b.run} ──`]);
    }
    fwLines += b.items.length + b.dropped;
    $("#fwCount").textContent = fwLines.toLocaleString();
    fwView.add(b.items);
  }),
);

// The QEMU monitor.
const monLog = $("#monLog");
async function monitor(cmd: string) {
  monLog.insertAdjacentHTML("beforeend", `<span class="cmd">(qemu) ${esc(cmd)}</span>\n`);
  try {
    monLog.insertAdjacentText("beforeend", (await Camera.monitor(cmd)) || "\n");
  } catch (e) {
    monLog.insertAdjacentHTML("beforeend", `<span class="bad">${esc(String(e))}</span>\n`);
  }
  monLog.scrollTop = monLog.scrollHeight;
}
$("#monForm").addEventListener("submit", (e) => {
  e.preventDefault();
  const inp = $<HTMLInputElement>("#monInput");
  if (inp.value.trim()) monitor(inp.value.trim());
  inp.value = "";
});
for (const c of document.querySelectorAll<HTMLButtonElement>("#monChips .chip")) c.addEventListener("click", () => monitor(c.textContent!));

// The run: where its files are and how QEMU was started.
let config: Awaited<ReturnType<typeof Camera.config>> | null = null;
Camera.config().then((c) => {
  config = c;
  renderRunInfo();
});
function renderRunInfo() {
  const s = status;
  const row = (k: string, v: string | undefined | number) =>
    v ? `<dt>${k}</dt><dd><code>${esc(String(v))}</code></dd>` : "";
  $("#runInfo").innerHTML = `
    <dl>
      ${row("run", s.run)}${row("state", s.state + (s.detail ? " -- " + s.detail : ""))}${row("pid", s.pid)}
      ${row("started", s.started ? new Date(s.started).toLocaleString() : "")}
      ${row("serial", s.files?.serial)}${row("stderr", s.files?.log)}${row("z63log", s.files?.z63log)}
      ${row("live frame", s.files?.live)}${row("QMP", s.files?.qmp)}${row("gdb", s.files?.gdb)}
      ${row("QEMU", config?.qemu)}${row("working dir", config?.dir)}${row("SNOR_USER", config?.snor)}${row("NAND", config?.nand)}
    </dl>
    ${s.cmdline ? `<h3>command line</h3><pre class="cmdline">${esc(s.cmdline.map((a) => (/[\s,]/.test(a) ? `'${a}'` : a)).join(" "))}</pre>` : ""}`;
}

showTab(tab);
