import "./styles.css";
import { pruneCharts, redrawAll } from "./chart";
import { delegate, esc, icons, patch, toast, type Handlers } from "./dom";
import { C, ms } from "./fmt";
import { model, type Route } from "./state";
import { bannerHandlers, startUpdater, updateBanner } from "./updater";
import { avgLastRtt, dropStacks, statusDot } from "./views/common";
import { nodeView } from "./views/node";
import { overviewView } from "./views/overview";
import { incidentsView, settingsView } from "./views/pages";
import type { View } from "./views/view";
import { Wizard } from "./views/wizard";
import { servicesView } from "./views/services";
import { services } from "./servicesModel";
import { runSelftest } from "./selftest";

const root = document.getElementById("app")!;
root.innerHTML = `<aside class="sidebar"></aside><div class="vline"></div><main class="main"><div class="banners"></div><div class="view"></div></main>`;
const side = root.querySelector<HTMLElement>(".sidebar")!;
const banners = root.querySelector<HTMLElement>(".banners")!;
const viewBox = root.querySelector<HTMLElement>(".view")!;

let view: View | null = null;
let viewKey = "";
let wizard: Wizard | null = null;

function routeKey(r: Route) { return r.kind === "node" ? `node:${r.id}` : r.kind; }
const isRoute = (r: Route) => routeKey(model.route) === routeKey(r);

function sidebar() {
  const openInc = model.nodes.flatMap((n) => n.incidents).filter((i) => i.endedAt == null).length;
  const item = (label: string, ic: string, r: Route, badge = 0) =>
    `<button class="nav ${isRoute(r) ? "on" : ""}" data-act="go" data-r="${esc(JSON.stringify(r))}">${ic}<span>${label.toUpperCase()}</span><span class="grow"></span>${badge ? `<span class="badge">${badge}</span>` : ""}</button>`;
  const nodes = model.nodes.map((n) => {
    const sel = isRoute({ kind: "node", id: n.id });
    const avg = avgLastRtt(n);
    return `<button class="noderow ${sel ? "on" : ""}" data-act="go" data-r="${esc(JSON.stringify({ kind: "node", id: n.id }))}">
      ${statusDot(n)}<span class="col grow ell"><span class="nname ell">${esc(n.name)}</span>
      <span class="mono9 ${n.reachable === false ? "red" : "faint"}">${n.reachable === false ? "UNREACHABLE" : n.unauthorized ? "NOT PAIRED" : `${n.targets.length} targets`}</span></span>
      ${avg != null ? `<span class="mono10 dim">${ms(avg)}</span>` : ""}</button>`;
  }).join("");
  patch(side, `<div class="brand"><span style="color:${C.amber}">${icons.pulse(16)}</span><span>INTERNET MONITOR</span></div>
    ${item("Overview", icons.grid(15), { kind: "overview" })}
    ${item("Incidents", icons.alert(15), { kind: "incidents" }, openInc)}
    ${item("Services", icons.cloud(15), { kind: "services" }, services.badCount)}
    <div class="sidehdr">NODES</div>
    <div class="nodelist scroll" data-scroll="nodes">${nodes || `<div class="mono10 faint pad16">No nodes yet</div>`}</div>
    <div class="pad12"><button class="btn wide" data-act="add-nodes">${icons.plus(13)} ADD NODES</button></div>
    ${item("Settings", icons.gear(15), { kind: "settings" })}`);
}

function ensureView() {
  let key = routeKey(model.route);
  if (model.route.kind === "node" && !model.node(model.route.id)) { model.route = { kind: "overview" }; key = "overview"; }
  if (key === viewKey && view) return;
  view?.destroy?.();
  viewBox.innerHTML = "";
  const r = model.route;
  view = r.kind === "overview" ? overviewView() : r.kind === "node" ? nodeView(model.node(r.id)!) : r.kind === "incidents" ? incidentsView() : r.kind === "services" ? servicesView() : settingsView();
  viewKey = key;
  viewBox.appendChild(view.el);
}

function render() {
  sidebar();
  banners.innerHTML = updateBanner();
  ensureView();
  view!.update();
  if (model.wizard.open && !wizard) {
    wizard = new Wizard(model.wizard.editing, () => { wizard = null; model.wizard = { open: false, editing: false }; model.changed(); });
    document.body.appendChild(wizard.el);
  }
  dropStacks(new Set(model.nodes.map((n) => n.id)));
  pruneCharts();
  redrawAll();
}

const handlers = (): Handlers => ({
  go: (e) => model.go(JSON.parse(e.dataset.r!)),
  "add-nodes": () => { model.wizard = { open: true, editing: false }; model.changed(); },
  ...bannerHandlers,
  ...(view?.handlers() ?? {}),
});
delegate(root, handlers);

document.addEventListener("contextmenu", (e) => {
  // keep the native menu for text fields only
  if (!(e.target as HTMLElement).closest("input, textarea, .sel, pre")) e.preventDefault();
});

async function boot() {
  try {
    await model.init();
    services.init();
  } catch (e) {
    toast("Startup failed: " + String(e), 10000);
  }
  model.subscribe(render);
  render();
  model.start();
  services.start();
  startUpdater();
  if (model.info.selftest) runSelftest(() => wizard);
}

boot();
