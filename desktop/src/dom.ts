// Tiny DOM toolkit: escaping, focus-preserving re-render, event delegation, dialogs, icons.

export function esc(v: unknown): string {
  return String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

/** Replaces `el`'s content, preserving focus/selection of the element with the same `data-k`
 *  and the scroll offset of elements carrying `data-scroll`. */
export function patch(el: HTMLElement, html: string, after?: () => void) {
  const active = document.activeElement as HTMLInputElement | null;
  const key = active && el.contains(active) ? active.dataset.k : undefined;
  const sel = key && "selectionStart" in active! ? [active!.selectionStart, active!.selectionEnd] : null;
  const scrollEls = (): HTMLElement[] => [...(el.dataset.scroll ? [el] : []), ...el.querySelectorAll<HTMLElement>("[data-scroll]")];
  const scrolls = new Map<string, [number, number]>();
  scrollEls().forEach((s) => scrolls.set(s.dataset.scroll!, [s.scrollTop, s.scrollLeft]));
  el.innerHTML = html;
  after?.();
  scrollEls().forEach((s) => {
    const v = scrolls.get(s.dataset.scroll!);
    if (v) { s.scrollTop = v[0]; s.scrollLeft = v[1]; }
  });
  if (key) {
    const n = el.querySelector<HTMLInputElement>(`[data-k="${CSS.escape(key)}"]`);
    if (n) {
      n.focus();
      if (sel && typeof sel[0] === "number") { try { n.setSelectionRange(sel[0], sel[1]); } catch { /* not a text input */ } }
    }
  }
}

/** Re-inserts persistent components into `[data-slot=...]` placeholders after a patch. */
export function fillSlots(root: HTMLElement, get: (slot: string) => HTMLElement | undefined) {
  root.querySelectorAll<HTMLElement>("[data-slot]").forEach((ph) => {
    const c = get(ph.dataset.slot!);
    if (!c) return;
    ph.replaceWith(c);
  });
}

export type Handler = (el: HTMLElement, ev: Event) => void;
export type Handlers = Record<string, Handler>;

/** Delegates click (`data-act`), input/change (`data-in`) and Enter (`data-enter`) to handlers. */
export function delegate(root: HTMLElement, handlers: () => Handlers) {
  root.addEventListener("click", (ev) => {
    const el = (ev.target as HTMLElement).closest<HTMLElement>("[data-act]");
    if (!el || !root.contains(el) || el.hasAttribute("disabled")) return;
    const h = handlers()[el.dataset.act!];
    if (h) { ev.preventDefault(); ev.stopPropagation(); h(el, ev); }
  });
  const onInput = (ev: Event) => {
    const el = (ev.target as HTMLElement).closest<HTMLElement>("[data-in]");
    if (!el || !root.contains(el)) return;
    handlers()[el.dataset.in!]?.(el, ev);
  };
  root.addEventListener("input", onInput);
  root.addEventListener("change", (ev) => {
    const el = ev.target as HTMLInputElement;
    if (el.type === "checkbox" || el.tagName === "SELECT") onInput(ev);
  });
  root.addEventListener("keydown", (ev) => {
    if ((ev as KeyboardEvent).key !== "Enter") return;
    const el = (ev.target as HTMLElement).closest<HTMLElement>("[data-enter]");
    if (!el || !root.contains(el)) return;
    ev.preventDefault();
    handlers()[el.dataset.enter!]?.(el, ev);
  });
}

// ---------- dialogs ----------

export function confirmDialog(opts: { title: string; message?: string; confirm: string; danger?: boolean; cancel?: string }): Promise<boolean> {
  return new Promise((resolve) => {
    const back = document.createElement("div");
    back.className = "modal-back";
    back.innerHTML = `<div class="modal" role="dialog" aria-modal="true">
      <div class="modal-title">${esc(opts.title)}</div>
      ${opts.message ? `<div class="modal-msg">${esc(opts.message)}</div>` : ""}
      <div class="row end gap8 mt16">
        <button class="btn" data-r="0">${esc(opts.cancel ?? "CANCEL")}</button>
        <button class="btn ${opts.danger ? "danger-solid" : "primary"}" data-r="1">${esc(opts.confirm)}</button>
      </div></div>`;
    const done = (v: boolean) => { back.remove(); document.removeEventListener("keydown", key); resolve(v); };
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") done(false); };
    back.addEventListener("click", (e) => {
      const b = (e.target as HTMLElement).closest<HTMLElement>("[data-r]");
      if (b) done(b.dataset.r === "1");
      else if (e.target === back) done(false);
    });
    document.addEventListener("keydown", key);
    document.body.appendChild(back);
    back.querySelector<HTMLButtonElement>('[data-r="0"]')?.focus();
  });
}

let toastTimer: number | undefined;
export function toast(msg: string, ms = 3500) {
  let t = document.getElementById("toast");
  if (!t) { t = document.createElement("div"); t.id = "toast"; document.body.appendChild(t); }
  t.textContent = msg;
  t.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => t!.classList.remove("show"), ms);
}

// ---------- icons (inline SVG, stroke = currentColor) ----------

const svg = (body: string, size = 16) =>
  `<svg class="ico" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${body}</svg>`;

export const icons = {
  pulse: (s?: number) => svg(`<path d="M2 12h4l2-6 4 13 3-9 2 2h5"/>`, s),
  grid: (s?: number) => svg(`<rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>`, s),
  alert: (s?: number) => svg(`<path d="M12 3 2 21h20L12 3z"/><path d="M12 10v5M12 18h.01"/>`, s),
  gear: (s?: number) => svg(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>`, s),
  plus: (s?: number) => svg(`<path d="M12 5v14M5 12h14"/>`, s),
  chevron: (s?: number) => svg(`<path d="m9 6 6 6-6 6"/>`, s),
  open: (s?: number) => svg(`<path d="M14 4h6v6M20 4l-9 9M18 14v6H4V6h6"/>`, s),
  cpu: (s?: number) => svg(`<rect x="6" y="6" width="12" height="12" rx="1"/><rect x="9.5" y="9.5" width="5" height="5"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>`, s),
  laptop: (s?: number) => svg(`<rect x="4" y="5" width="16" height="11" rx="1"/><path d="M2 19h20"/>`, s),
  router: (s?: number) => svg(`<rect x="3" y="13" width="18" height="7" rx="1"/><path d="M7 16.5h.01M11 16.5h.01M17 13V9M8.5 8.5a5 5 0 0 1 7 0M6 6a8.5 8.5 0 0 1 12 0"/>`, s),
  server: (s?: number) => svg(`<rect x="3" y="3" width="18" height="7" rx="1"/><rect x="3" y="14" width="18" height="7" rx="1"/><path d="M7 6.5h.01M7 17.5h.01"/>`, s),
  cloud: (s?: number) => svg(`<path d="M7 18a5 5 0 1 1 1-9.9A6 6 0 0 1 19.5 10 4 4 0 0 1 18 18H7z"/>`, s),
  desktop: (s?: number) => svg(`<rect x="3" y="4" width="18" height="12" rx="1"/><path d="M8 20h8M12 16v4"/>`, s),
  check: (s?: number) => svg(`<path d="m5 12 5 5L20 7"/>`, s),
  x: (s?: number) => svg(`<path d="M6 6l12 12M18 6 6 18"/>`, s),
  trash: (s?: number) => svg(`<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>`, s),
  key: (s?: number) => svg(`<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3"/>`, s),
  warn: (s?: number) => svg(`<circle cx="12" cy="12" r="9"/><path d="M12 7v6M12 16.5h.01"/>`, s),
  ok: (s?: number) => svg(`<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>`, s),
  dots: (s?: number) => svg(`<circle cx="12" cy="12" r="9"/><path d="M8 12h.01M12 12h.01M16 12h.01"/>`, s),
  table: (s?: number) => svg(`<rect x="3" y="4" width="18" height="16" rx="1"/><path d="M3 10h18M3 15h18M9 4v16"/>`, s),
  image: (s?: number) => svg(`<rect x="3" y="4" width="18" height="16" rx="1"/><circle cx="9" cy="10" r="2"/><path d="m21 17-5-5-9 8"/>`, s),
  github: (s?: number) => svg(`<path d="M9 19c-4 1.5-4-2-6-2.5m12 5v-3.5a3 3 0 0 0-.9-2.3c3-.3 6.1-1.5 6.1-6.6a5.2 5.2 0 0 0-1.4-3.6 4.8 4.8 0 0 0-.1-3.6s-1.1-.3-3.7 1.4a12.6 12.6 0 0 0-6.6 0C5.8 1.6 4.7 1.9 4.7 1.9a4.8 4.8 0 0 0-.1 3.6A5.2 5.2 0 0 0 3.2 9c0 5.1 3.1 6.3 6.1 6.6a3 3 0 0 0-.9 2.3V21"/>`, s),
  download: (s?: number) => svg(`<path d="M12 3v12M7 10l5 5 5-5M4 21h16"/>`, s),
};

export const nodeIconChoices: [keyof typeof icons, string][] = [
  ["cpu", "Raspberry Pi"], ["laptop", "Computer"], ["router", "Router"], ["server", "Server"], ["cloud", "Cloud"],
];

export function dot(color: string, size = 8, glow = true): string {
  return `<span class="dot" style="width:${size}px;height:${size}px;background:${color};${glow ? `box-shadow:0 0 4px ${color}b3` : ""}"></span>`;
}

export function spinner(): string {
  return `<span class="spin"></span>`;
}
