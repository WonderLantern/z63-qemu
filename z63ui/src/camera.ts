// The body: its back with the live monitor, and its top with the control
// panel, each a picture (skin.ts) with its buttons as hot spots over it.
import { hold, release, onHeld } from "./keys";
import { back, top, type Hot } from "./skin";
import { attachTouch } from "./touch";

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, html?: string) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html !== undefined) e.innerHTML = html;
  return e;
};

// A button held while the pointer is down on it.
function holdOnPress(node: HTMLElement, key: () => string | null) {
  let held: string | null = null;
  node.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    node.setPointerCapture(e.pointerId);
    held = key();
    if (held) hold(held);
  });
  const up = () => {
    if (held) release(held);
    held = null;
  };
  node.addEventListener("pointerup", up);
  node.addEventListener("pointercancel", up);
  node.addEventListener("lostpointercapture", up);
}

function place(e: HTMLElement, h: { x: number; y: number; r: number }) {
  e.style.setProperty("--x", String(h.x));
  e.style.setProperty("--y", String(h.y));
  e.style.setProperty("--r", String(h.r));
}

function hot(h: Hot): HTMLElement {
  const b = el("button", "hot");
  b.type = "button";
  place(b, h);
  b.title = h.title;
  if (h.key) {
    b.dataset.key = h.key;
    holdOnPress(b, () => h.key!);
  } else {
    b.classList.add("dead");
  }
  return b;
}

function picture(host: HTMLElement, src: string, w: number, h: number) {
  host.style.width = w + "px";
  host.style.height = h + "px";
  const img = el("img", "picture") as HTMLImageElement;
  img.src = src;
  img.draggable = false;
  img.alt = "";
  host.append(img);
}

// Every hot spot with a key lights while that key is held.
function lightHeld(host: HTMLElement, more?: (keys: Set<string>) => void) {
  onHeld((keys) => {
    for (const b of host.querySelectorAll<HTMLElement>("[data-key]")) b.classList.toggle("held", keys.has(b.dataset.key!));
    more?.(keys);
  });
}

// Which way the pointer is from the element's centre, at the last
// pointerdown; centre (when given) inside 42% of the radius.
let lastPointer = { x: 0, y: 0 };
document.addEventListener("pointerdown", (e) => (lastPointer = { x: e.clientX, y: e.clientY }), true);
function direction(node: HTMLElement, [u, d, l, r]: string[], centre: string | null): string | null {
  const b = node.getBoundingClientRect();
  const dx = lastPointer.x - (b.left + b.width / 2);
  const dy = lastPointer.y - (b.top + b.height / 2);
  const rad = Math.hypot(dx, dy) / (b.width / 2);
  if (centre && rad < 0.42) return centre;
  if (!centre && rad < 0.36) return null; // OK sits there
  return Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? r : l)! : (dy > 0 ? d : u)!;
}

// The back: the monitor (a canvas, and a touch screen), the buttons, the
// multi selector's four directions and the sub-selector's five.
export function buildBack(host: HTMLElement): HTMLCanvasElement {
  picture(host, back.image, back.w, back.h);
  const screen = el("div", "screen");
  const canvas = el("canvas", "lcd") as HTMLCanvasElement;
  canvas.width = 1024;
  canvas.height = 682;
  screen.append(canvas, el("div", "glare"), el("div", "note"));
  const r = back.screen;
  Object.assign(screen.style, { left: r.x + "px", top: r.y + "px", width: r.w + "px", height: r.h + "px" });
  host.append(screen);
  attachTouch(canvas);

  for (const d of back.dead) host.append(hot(d));
  const ring = el("div", "hring");
  place(ring, back.ring);
  ring.title = "Multi selector (arrow keys)";
  holdOnPress(ring, () => direction(ring, ["up", "down", "left", "right"], null));
  const stick = el("div", "hring");
  place(stick, back.stick);
  stick.title = "Sub-selector: tilt, or press in the middle";
  holdOnPress(stick, () => direction(stick, ["ss-up", "ss-down", "ss-left", "ss-right"], "ss-center"));
  host.append(ring, stick);
  for (const b of back.buttons) host.append(hot(b));

  lightHeld(host, (keys) => {
    const dir = (names: string[]) => names.find((k) => keys.has(k));
    ring.dataset.dir = dir(["up", "down", "left", "right"]) ?? "";
    stick.dataset.dir = (dir(["ss-up", "ss-down", "ss-left", "ss-right", "ss-center"]) ?? "").replace("ss-", "");
  });
  return canvas;
}

// The top: the shutter-release button, the buttons around it, and the
// control panel, a canvas runPanel draws (click it to see it larger).
export function buildTop(host: HTMLElement): HTMLCanvasElement {
  picture(host, top.image, top.w, top.h);
  const p = top.panel;
  const panel = el("div", "cpanel");
  const lcd = el("canvas") as HTMLCanvasElement;
  lcd.width = lcd.height = 128;
  panel.append(lcd, el("div", "cpanel-note"));
  Object.assign(panel.style, { left: p.x + "px", top: p.y + "px", width: p.w + "px", height: p.h + "px" });
  panel.title = "Control panel: the 128×128 memory LCD the body MCU draws over SPI2 -- click to enlarge";
  panel.addEventListener("click", () => panel.classList.toggle("big"));
  host.append(panel);
  for (const d of top.dead) host.append(hot(d));
  for (const b of top.buttons) host.append(hot(b));

  // A click is a full press: half at once, full a moment later, as a finger
  // goes down; with Shift it stops at half.
  const sh = hot({ ...top.shutter, key: undefined });
  sh.classList.remove("dead");
  sh.classList.add("shutter");
  let timer = 0;
  let held: string | null = null;
  sh.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    sh.setPointerCapture(e.pointerId);
    held = "half";
    hold("half");
    if (!e.shiftKey) {
      timer = window.setTimeout(() => {
        hold("full");
        release("half");
        held = "full";
      }, 180);
    }
  });
  const up = () => {
    clearTimeout(timer);
    if (held) release(held);
    held = null;
  };
  sh.addEventListener("pointerup", up);
  sh.addEventListener("pointercancel", up);
  sh.addEventListener("lostpointercapture", up);
  host.append(sh);

  lightHeld(host, (keys) => {
    sh.classList.toggle("half", keys.has("half") && !keys.has("full"));
    sh.classList.toggle("full", keys.has("full"));
  });
  return lcd;
}

// What is on neither picture: the front's Fn buttons, and what is held.
export function buildFront(host: HTMLElement) {
  for (const [key, label, k] of [["fn", "Fn1", "F1"], ["fn2", "Fn2", "F2"]] as const) {
    const b = el("button", "fbtn", label);
    b.type = "button";
    b.dataset.key = key;
    b.title = `${label} button, on the front (${k})`;
    holdOnPress(b, () => key);
    host.append(b);
  }
  const heldEl = el("span", "keys-hint");
  host.append(heldEl);
  lightHeld(host, (keys) => {
    heldEl.textContent = keys.size ? "holding " + [...keys].join(" + ") : "click a button to press it · shortcuts in each tooltip";
  });
}

// The 36-byte header live.go puts before a picture.
function frameHeader(buf: ArrayBuffer) {
  const dv = new DataView(buf);
  const nl = dv.getUint32(32, true);
  return {
    pixels: (dv.getUint32(4, true) & 1) !== 0,
    seq: dv.getBigUint64(8, true),
    w: dv.getUint32(16, true),
    h: dv.getUint32(20, true),
    count: Number(dv.getBigUint64(24, true)),
    note: new TextDecoder().decode(new Uint8Array(buf, 36, nl)),
    at: 36 + nl,
  };
}

// The control panel: frames from frame://localhost/top/next (live.go) --
// the panel's own 128×128 in its own order (nikon-z63-toplcd.h), turned a
// quarter clockwise as it sits in the body, white pixels light on a dark
// ground. The counters are for the header.
const PAPER = [11, 12, 14, 255];
const INK = [228, 231, 226, 255];
export function runPanel(canvas: HTMLCanvasElement) {
  const ctx = canvas.getContext("2d")!;
  const src = document.createElement("canvas");
  const sctx = src.getContext("2d")!;
  const note = canvas.parentElement!.querySelector<HTMLElement>(".cpanel-note")!;
  const stats = { shown: 0, packets: 0, note: "" };
  let seq = 0n;
  const loop = async () => {
    for (;;) {
      try {
        const r = await fetch(`frame://localhost/top/next?after=${seq}`, { cache: "no-store" });
        const buf = await r.arrayBuffer();
        const f = frameHeader(buf);
        seq = f.seq;
        stats.packets = f.count;
        stats.note = f.note;
        if (f.pixels) {
          const px = new Uint8Array(buf, f.at, f.w * f.h * 4);
          const img = sctx.createImageData(f.w, f.h);
          // The note says when DISP is low: the panel shows nothing.
          for (let i = 0; i < px.length; i += 4) img.data.set(px[i]! > 127 && !f.note ? INK : PAPER, i);
          src.width = f.w;
          src.height = f.h;
          sctx.putImageData(img, 0, 0);
          canvas.width = f.h;
          canvas.height = f.w;
          ctx.setTransform(0, 1, -1, 0, f.h, 0); // (x, y) -> (h - y, x)
          ctx.drawImage(src, 0, 0);
          stats.shown++;
        }
        note.textContent = f.w ? (f.note ? "blank: DISP low" : "") : "no panel yet";
      } catch {
        await new Promise((res) => setTimeout(res, 500));
      }
    }
  };
  loop();
  return stats;
}

// The monitor: frames from frame://localhost/next (live.go), drawn as they
// change. Returns the counters the header shows.
export function runMonitor(canvas: HTMLCanvasElement, zoom: HTMLCanvasElement, zoomOpen: () => boolean) {
  const ctx = canvas.getContext("2d")!;
  const zctx = zoom.getContext("2d")!;
  const note = canvas.parentElement!.querySelector<HTMLElement>(".note")!;
  const stats = { shown: 0, composed: 0, w: 0, h: 0, note: "" };
  let seq = 0n;
  const loop = async () => {
    for (;;) {
      try {
        const r = await fetch(`frame://localhost/next?after=${seq}`, { cache: "no-store" });
        const buf = await r.arrayBuffer();
        const { pixels, w, h, count, note: text, at, ...f } = frameHeader(buf);
        seq = f.seq;
        stats.composed = count;
        stats.note = text;
        if (pixels) {
          const img = new ImageData(new Uint8ClampedArray(buf, at, w * h * 4), w, h);
          for (const [c, x] of [[canvas, ctx], [zoom, zctx]] as const) {
            if (c === zoom && !zoomOpen()) continue;
            if (c.width !== w || c.height !== h) {
              c.width = w;
              c.height = h;
            }
            x.putImageData(img, 0, 0);
          }
          stats.shown++;
          stats.w = w;
          stats.h = h;
        }
        note.textContent = stats.note && !stats.w ? stats.note : "";
      } catch {
        await new Promise((res) => setTimeout(res, 500));
      }
    }
  };
  loop();
  return stats;
}
