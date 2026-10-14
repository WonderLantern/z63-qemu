// The buttons held right now, from the pointer and the keyboard together,
// sent whole to the machine (qom-set /machine mcu-keys) on every change.
import { Camera } from "./mygo";
import { toast } from "./util";

const held = new Map<string, number>(); // key -> holders
const listeners: ((keys: Set<string>) => void)[] = [];
let seq = 0;

function push() {
  const keys = [...held.keys()];
  // Calls run on goroutines of their own on the Go side; the sequence lets
  // it drop one that arrives after a newer one. Time-based, so a reloaded
  // page still counts up.
  seq = Math.max(seq + 1, Date.now() * 1000);
  Camera.setKeys(seq, keys).catch((e) => toast(String(e), "error"));
  const set = new Set(keys);
  for (const fn of listeners) fn(set);
}

export function hold(key: string) {
  held.set(key, (held.get(key) ?? 0) + 1);
  push();
}

export function release(key: string) {
  const n = (held.get(key) ?? 0) - 1;
  if (n > 0) held.set(key, n);
  else if (held.delete(key)) push();
}

export function releaseAll() {
  if (held.size) {
    held.clear();
    push();
  }
}

export function onHeld(fn: (keys: Set<string>) => void) {
  listeners.push(fn);
  fn(new Set(held.keys()));
}
