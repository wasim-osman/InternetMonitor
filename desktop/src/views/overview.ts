import { esc, fillSlots, icons, patch, dot, type Handlers } from "../dom";
import { C, endpointKey, latencyColor, ms, pct, mean } from "../fmt";
import { model } from "../state";
import { avgLastRtt, panel, rangeBar, rangeHandlers, stackFor, statusDot } from "./common";
import type { View } from "./view";

export function overviewView(): View {
  const el = document.createElement("div");
  el.className = "page scroll";
  el.dataset.scroll = "page";

  const kpis = () => {
    const live = model.nodes.flatMap((n) => Object.values(n.live));
    const up = live.filter((l) => l.state === "up").length;
    const rtts = live.map((l) => l.avgMs).filter((v): v is number => typeof v === "number");
    const open = model.nodes.flatMap((n) => n.incidents).filter((i) => i.endedAt == null).length;
    const k = (l: string, v: string, c: string) => panel(null, `<div class="stat"><span>${esc(l.toUpperCase())}</span><b style="color:${c}">${v}</b></div>`, "", "kpi");
    return `<div class="kpis">
      ${k("Nodes online", `${model.nodes.filter((n) => n.reachable === true).length}/${model.nodes.length}`, C.green)}
      ${k("Links up", `${up}/${live.length}`, up === live.length ? C.green : C.amber)}
      ${k("Mean latency", rtts.length ? ms(mean(rtts)) + " ms" : "—", C.cyan)}
      ${k("Open incidents", String(open), open === 0 ? C.green : C.red)}
    </div>`;
  };

  const matrix = () => {
    const cols: { key: string; label: string }[] = [];
    const seen = new Set<string>();
    for (const n of model.nodes) for (const t of n.targets) { const k = endpointKey(t); if (!seen.has(k)) { seen.add(k); cols.push({ key: k, label: t.name }); } }
    const head = `<tr><th class="mnode"></th>${cols.map((c) => `<th title="${esc(c.label)}">${esc(c.label)}</th>`).join("")}</tr>`;
    const rows = model.nodes.map((n) => {
      const cells = cols.map((c) => {
        const t = n.targets.find((x) => endpointKey(x) === c.key);
        if (!t) return `<td class="mcell none">·</td>`;
        const l = n.live[t.id];
        const down = l?.state === "down";
        const col = down ? C.red : latencyColor(l?.lastRttMs);
        const tip = `${n.name} → ${t.name}: avg ${ms(l?.avgMs)} ms · loss ${pct(l?.lossPct)} · jitter ${ms(l?.jitterMs)} ms`;
        return `<td class="mcell" title="${esc(tip)}" style="color:${col};background:${col}${l ? "38" : "0d"}">${down ? "DOWN" : ms(l?.lastRttMs)}</td>`;
      }).join("");
      return `<tr><td class="mnode"><a data-act="open-node" data-id="${esc(n.id)}">${statusDot(n, 6)}<span>${esc(n.name)}</span></a></td>${cells}</tr>`;
    }).join("");
    return `<div class="mwrap" data-scroll="matrix"><table class="matrix">${head}${rows}</table></div>`;
  };

  const sections = () => model.nodes.map((n) => {
    const open = model.expanded.has(n.id);
    const rtt = avgLastRtt(n);
    const body = !open ? "" : n.targets.length === 0
      ? `<div class="empty">No targets configured</div>`
      : `<div class="secscroll" data-scroll="sec-${esc(n.id)}" style="max-height:460px"><div data-slot="stack-${esc(n.id)}"></div></div>`;
    return `<div class="section ${open ? "open" : ""}">
      <div class="sechead" data-act="toggle" data-id="${esc(n.id)}">
        <span class="chev">${icons.chevron(14)}</span>${statusDot(n)}
        <b>${esc(n.name)}</b><span class="mono10 faint">${esc(n.saved.host)}</span>
        <span class="grow"></span>
        ${n.reachable === false ? `<span class="mono9 red bold">UNREACHABLE</span>` : ""}
        <span class="mono9 faint bold">${n.targets.length} TARGETS</span>
        ${rtt != null ? `<span class="mono10 dim bold">AVG ${ms(rtt)} ms</span>` : ""}
        <button class="iconbtn" title="Open node" data-act="open-node" data-id="${esc(n.id)}">${icons.open(15)}</button>
      </div>${body}</div>`;
  }).join("");

  const cards = () => `<div class="cards">${model.nodes.map((n) => {
    const ts = n.targets.slice(0, 6).map((t) => {
      const l = n.live[t.id];
      const down = l?.state === "down";
      return `<div class="row gap6"><span>${dot(l?.state === "up" ? C.green : l?.state === "degraded" ? C.amber : down ? C.red : C.faint, 6)}</span><span class="grow ell small">${esc(t.name)}</span><b class="mono11" style="color:${down ? C.red : latencyColor(l?.lastRttMs)}">${down ? "DOWN" : ms(l?.lastRttMs)}</b></div>`;
    }).join("");
    const more = n.targets.length > 6 ? `<div class="mono9 faint">+${n.targets.length - 6} more</div>` : "";
    const none = n.targets.length === 0 ? `<div class="small faint">${n.reachable === false ? "Unreachable" : "No targets"}</div>` : "";
    return `<div class="card" data-act="open-node" data-id="${esc(n.id)}">${panel(null, `<div class="row gap8 mb8">${statusDot(n)}<b class="grow">${esc(n.name)}</b><span class="mono10 faint">${esc(n.saved.host)}</span></div><div class="col gap6">${ts}${more}${none}</div>`)}</div>`;
  }).join("")}</div>`;

  const update = () => {
    if (!model.nodes.length) {
      patch(el, `<div class="pad">${kpis()}${panel(null, `<div class="center col gap10" style="min-height:160px"><span class="dim">No nodes yet</span><button class="btn primary" data-act="wizard">RUN SETUP WIZARD</button></div>`)}</div>`);
      return;
    }
    patch(el, `<div class="pad col gap16">
      ${kpis()}
      ${panel("Latency matrix — node × target (ms)", matrix())}
      <div class="row gap12 wrap"><span class="mono10 bold dim track">GRAPHS</span>${rangeBar()}<span class="grow"></span>
        <button class="btn" data-act="expand-all">EXPAND ALL</button><button class="btn" data-act="collapse-all">COLLAPSE ALL</button></div>
      ${sections()}
      ${cards()}
    </div>`, () => fillSlots(el, (slot) => {
      const n = model.node(slot.replace(/^stack-/, ""));
      return n ? stackFor(n).el : undefined;
    }));
  };

  const handlers = (): Handlers => ({
    ...rangeHandlers(),
    "open-node": (e) => model.go({ kind: "node", id: e.dataset.id! }),
    toggle: (e) => { const n = model.node(e.dataset.id!); if (n) model.toggleExpanded(n); },
    "expand-all": () => model.setAllExpanded(true),
    "collapse-all": () => model.setAllExpanded(false),
    wizard: () => { model.wizard = { open: true, editing: false }; model.changed(); },
  });

  return { el, update, handlers };
}
