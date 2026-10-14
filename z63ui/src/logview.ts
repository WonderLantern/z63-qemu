// A scrolling log in a <pre>: keeps the last `cap` lines, shows the last
// `show` that pass the filter, at most a few times a second. Scrolling up
// stops following; the Follow button (or scrolling to the end) resumes.
import { esc } from "./util";

export interface LogViewOptions {
  cap: number;
  show: number;
  fmt?: (line: string) => string; // HTML for one line (already escaped)
  follow?: HTMLButtonElement;
  visible?: () => boolean;
}

export class LogView {
  lines: string[] = [];
  partial = ""; // the last line, until its newline comes
  filter: ((s: string) => boolean) | null = null;
  following = true;
  private dirty = false;
  private unseen = 0;
  private last = 0;
  private programmatic = false;

  constructor(
    public el: HTMLElement,
    private o: LogViewOptions,
  ) {
    el.addEventListener("scroll", () => {
      if (this.programmatic) return;
      const atEnd = el.scrollTop + el.clientHeight >= el.scrollHeight - 30;
      if (atEnd !== this.following) {
        this.following = atEnd;
        if (atEnd) this.mark();
        this.updateButton();
      }
    });
    o.follow?.addEventListener("click", () => {
      this.following = true;
      this.updateButton();
      this.mark(true);
    });
    this.updateButton();
  }

  add(lines: string[]) {
    for (const l of lines) this.lines.push(l);
    if (this.lines.length > this.o.cap * 1.25) this.lines = this.lines.slice(-this.o.cap);
    if (!this.following) this.unseen += lines.length;
    this.mark();
  }

  clear() {
    this.lines = [];
    this.partial = "";
    this.unseen = 0;
    this.mark(true);
  }

  mark(force = false) {
    if (force) this.last = 0;
    if (this.dirty) return;
    this.dirty = true;
    requestAnimationFrame(() => this.render());
  }

  private updateButton() {
    const b = this.o.follow;
    if (!b) return;
    b.classList.toggle("on", this.following);
    b.textContent = this.following || !this.unseen ? "Follow" : `Follow (+${this.unseen})`;
  }

  render() {
    this.dirty = false;
    if (this.o.visible && !this.o.visible()) return; // mark() again when shown
    if (!this.following) {
      this.updateButton();
      return; // keep the reader's place
    }
    const now = performance.now();
    if (now - this.last < 150) {
      this.dirty = true;
      setTimeout(() => this.render(), 150 - (now - this.last));
      return;
    }
    this.last = now;
    this.unseen = 0;
    const out: string[] = [];
    const all = this.partial ? [...this.lines.slice(-this.o.show * 4), this.partial] : this.lines;
    const fmt = this.o.fmt ?? esc;
    for (let i = all.length - 1; i >= 0 && out.length < this.o.show; i--) {
      const l = all[i]!;
      if (!this.filter || this.filter(l)) out.push(fmt(l));
    }
    out.reverse();
    this.el.innerHTML = out.join("\n");
    this.programmatic = true;
    this.el.scrollTop = this.el.scrollHeight;
    requestAnimationFrame(() => (this.programmatic = false));
    this.updateButton();
  }
}

// Raw UART text into lines: CR LF and lone CRs, backspaces, and the escape
// sequences BusyBox's line editing and ls --color send.
export class Terminal {
  constructor(private view: LogView) {}

  write(text: string) {
    text = text
      .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "")
      .replace(/\x1b[()][0-9A-Za-z]/g, "")
      .replace(/\x1b[=>78DEHM]/g, "")
      .replace(/\r\n/g, "\n");
    const parts = (this.view.partial + text).split("\n");
    this.view.partial = clean(parts.pop()!);
    this.view.add(parts.map(clean));
  }
}

function clean(line: string): string {
  if (line.includes("\r")) {
    const segs = line.split("\r").filter((s) => s.length);
    line = segs.length ? segs[segs.length - 1]! : "";
  }
  if (line.includes("\b")) {
    const out: string[] = [];
    for (const c of line) c === "\b" ? out.pop() : out.push(c);
    line = out.join("");
  }
  // eslint-disable-next-line no-control-regex
  return line.replace(/[\x00-\x08\x0b\x0c\x0e-\x1f]/g, "");
}
