// The monitor as a touch screen: pointers on the picture become contacts
// (Camera.Touch -> QEMU multi-touch input, machine.go). Calls go one at a
// time, in order, with a contact's moves between two calls folded into its
// last position, so a begin is never overtaken by its own end.
import { Camera, type TouchPoint } from "./mygo";

const queue: TouchPoint[] = [];
let busy = false;
let lastError = "";
let onError: (msg: string) => void = () => {};

async function flush() {
  if (busy || !queue.length) return;
  busy = true;
  const batch = queue.splice(0);
  try {
    await Camera.touch(batch);
    lastError = "";
  } catch (e) {
    const msg = String(e);
    if (msg !== lastError) onError(msg); // say each new reason once
    lastError = msg;
  }
  busy = false;
  flush();
}

function send(p: TouchPoint) {
  const last = queue[queue.length - 1];
  if (p.type === "update" && last?.type === "update" && last.slot === p.slot) queue[queue.length - 1] = p;
  else queue.push(p);
  flush();
}

export function onTouchError(fn: (msg: string) => void) {
  onError = fn;
}

let tracking = 0;
const slots = new Map<number, { slot: number; id: number; dot: HTMLElement }>(); // pointerId ->

export function attachTouch(surface: HTMLElement) {
  surface.style.touchAction = "none";
  const at = (e: PointerEvent) => {
    const b = surface.getBoundingClientRect();
    return { x: (e.clientX - b.left) / b.width, y: (e.clientY - b.top) / b.height };
  };
  surface.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    surface.setPointerCapture(e.pointerId);
    let slot = 0;
    while ([...slots.values()].some((s) => s.slot === slot)) slot++;
    const p = at(e);
    const dot = document.createElement("i");
    dot.className = "touchdot";
    surface.parentElement!.append(dot);
    const s = { slot, id: ++tracking, dot };
    slots.set(e.pointerId, s);
    place(surface, dot, p);
    send({ type: "begin", slot, id: s.id, ...p });
  });
  surface.addEventListener("pointermove", (e) => {
    const s = slots.get(e.pointerId);
    if (!s) return;
    const p = at(e);
    place(surface, s.dot, p);
    send({ type: "update", slot: s.slot, id: s.id, ...p });
  });
  const end = (type: "end" | "cancel") => (e: PointerEvent) => {
    const s = slots.get(e.pointerId);
    if (!s) return;
    slots.delete(e.pointerId);
    s.dot.classList.add("up");
    setTimeout(() => s.dot.remove(), 300);
    send({ type, slot: s.slot, id: s.id, ...at(e) });
  };
  surface.addEventListener("pointerup", end("end"));
  surface.addEventListener("pointercancel", end("cancel"));
}

// The dot sits in the surface's parent, which the surface fills.
function place(surface: HTMLElement, dot: HTMLElement, p: { x: number; y: number }) {
  const x = Math.min(Math.max(p.x, 0), 1);
  const y = Math.min(Math.max(p.y, 0), 1);
  dot.style.left = `${surface.offsetLeft + x * surface.offsetWidth}px`;
  dot.style.top = `${surface.offsetTop + y * surface.offsetHeight}px`;
}
