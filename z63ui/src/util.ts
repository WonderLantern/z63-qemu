export const $ = <T extends HTMLElement = HTMLElement>(sel: string) => document.querySelector<T>(sel)!;

export const esc = (s: string) => s.replace(/[&<>]/g, (c) => (c === "&" ? "&amp;" : c === "<" ? "&lt;" : "&gt;"));

export function toast(msg: string, kind: "info" | "error" = "info") {
  const t = document.createElement("div");
  t.className = "toast " + kind;
  t.textContent = msg;
  $("#toasts").append(t);
  setTimeout(() => t.classList.add("out"), kind === "error" ? 6000 : 3500);
  setTimeout(() => t.remove(), kind === "error" ? 6600 : 4100);
}

export const store = {
  get(k: string): string | null {
    try {
      return localStorage.getItem("z63ui." + k);
    } catch {
      return null;
    }
  },
  set(k: string, v: string) {
    try {
      localStorage.setItem("z63ui." + k, v);
    } catch {}
  },
};

// A filter box's text: /regex/ or a case-insensitive substring.
export function matcher(text: string): ((s: string) => boolean) | null {
  text = text.trim();
  if (!text) return null;
  const m = /^\/(.+)\/([a-z]*)$/.exec(text);
  if (m) {
    try {
      const re = new RegExp(m[1]!, m[2]!.includes("i") ? m[2] : m[2] + "i");
      return (s) => re.test(s);
    } catch {
      return () => false;
    }
  }
  const t = text.toLowerCase();
  return (s) => s.toLowerCase().includes(t);
}
