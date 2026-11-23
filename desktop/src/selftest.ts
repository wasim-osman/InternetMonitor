// End-to-end self-test of the real UI code paths, driven inside the webview.
// Enabled only when the app is started with IM_SELFTEST=1 (see desktop/README.md); prints a JSON
// report on stdout ("SELFTEST {...}") via the selftest_report command.
import { Agent, api, errText } from "./api";
import { seriesCSV } from "./fmt";
import { model } from "./state";
import { stackFor } from "./views/common";
import type { Wizard } from "./views/wizard";
import { parseHostPort } from "./wizardLogic";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
async function until(cond: () => boolean, timeoutMs: number, step = 250) {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) { if (cond()) return true; await sleep(step); }
  return cond();
}
const text = (sel: string) => [...document.querySelectorAll<HTMLElement>(sel)].map((e) => e.innerText.replace(/\s+/g, " ").trim());
const click = (sel: string) => { const b = document.querySelector<HTMLElement>(sel); b?.click(); return !!b; };

export async function runSelftest(getWizard: () => Wizard | null) {
  const R: Record<string, unknown> = { version: model.info.version, platform: model.info.platform };
  const errors: string[] = [];
  const check = (name: string, ok: boolean, detail?: unknown) => { R[name] = detail ?? ok; if (!ok) errors.push(name); };
  try {
    const agentAddr = model.info.selftestAgent;
    // 1. wizard shows on first run and the discovery scan runs
    const w = getWizard();
    check("wizardShownOnFirstRun", !!w && model.nodes.length === 0);
    if (w && agentAddr) {
      await until(() => !w.scanning, 60000);
      check("scanCandidates", w.candidates.length >= 0, { count: w.candidates.length, agents: w.candidates.filter((c) => c.agent).map((c) => `${c.name}@${c.ip}`), progress: w.progress });
      // 2. manual add of the dev agent, connect, pair with a loopback code, targets grid, apply
      w.selected.clear();
      w.manual = agentAddr;
      await w.addManual();
      const { host, port } = parseHostPort(agentAddr);
      check("manualAdd", w.selected.has(host), { error: w.manualError, selected: [...w.selected] });
      w.beginConnect();
      await until(() => ["needsCode", "ready", "noAgent"].includes(w.conns[host]?.phase), 15000);
      const phase = w.conns[host]?.phase;
      if (phase === "needsCode") {
        const pc = await new Agent({ host, port }).req<{ code: string }>("POST", "/v1/local/pair-code", undefined, 5000, false);
        await w.pair(host, pc.code);
      }
      check("paired", w.conns[host]?.phase === "ready", { phaseBefore: phase, phase: w.conns[host]?.phase, msg: w.conns[host]?.message });
      w.buildRows(); w.step = 2; w.render();
      w.custom = { kind: "http", name: "Example", host: "example.com", port: "" };
      (w.handlers() as any)["w-cadd"]();
      check("targetsGrid", w.rows.length >= 4, { rows: w.rows.map((r) => `${r.section}:${r.template.name}`), cells: w.grid.size, gridDomCells: document.querySelectorAll(".wizard .cell").length });
      w.step = 3; w.render();
      const ok = await w.apply();
      check("applied", ok, w.applyResults);
      await until(() => getWizard() === null, 5000);
    }
    // 3. overview with real data
    model.go({ kind: "overview" });
    const n = model.nodes[0];
    check("nodeSaved", !!n, model.nodes.map((x) => ({ host: x.saved.host, port: x.saved.port, id: x.id })));
    if (!n) throw new Error("no node");
    await until(() => Object.keys(n.live).length > 0 && n.seriesLoaded && !!n.config, 40000);
    await sleep(2500);
    check("retention60", (n.config?.retentionRollupDays ?? 0) >= 60, n.config?.retentionRollupDays);
    check("liveTargets", Object.keys(n.live).length > 0, Object.values(n.live).map((l) => `${l.id}:${l.state}:${l.lastRttMs}`));
    check("kpis", text(".kpi").length === 4, text(".kpi"));
    const cells = text(".matrix td.mcell");
    check("matrix", cells.length > 0 && cells.some((c) => /\d/.test(c)), cells);
    const pts = Object.fromEntries(Object.entries(n.series).map(([k, s]) => [k, s.points.length]));
    check("seriesPoints", Object.values(pts).some((v) => v > 0), pts);
    const rows = document.querySelectorAll(".view .trow");
    const canv = [...document.querySelectorAll<HTMLCanvasElement>(".view .trow canvas")].map((c) => `${c.width}x${c.height}`);
    check("overviewCharts", rows.length === n.targets.length && rows.length > 0, { rows: rows.length, canvases: canv, info: text(".view .trow .tinfo").slice(0, 3) });
    check("sidebar", text(".noderow").length === model.nodes.length, text(".sidebar"));
    // range change re-fetches series
    model.setRange("5m");
    await sleep(2500);
    check("range5m", model.range === "5m" && n.seriesLoaded, Object.values(n.series).map((s) => `${s.target}:${s.stepSec}s/${s.points.length}`));
    model.setRange("1h");
    // 4. node page tabs
    model.go({ kind: "node", id: n.id });
    await sleep(2000);
    check("nodeHeader", text(".nhead").join(" ").includes(n.saved.host), text(".nhead"));
    check("nodeCharts", document.querySelectorAll(".view .trow").length === n.targets.length, document.querySelectorAll(".view .trow").length);
    const png = stackFor(n).toPNG(n.name);
    check("pngExport", png.startsWith("data:image/png;base64,") && png.length > 5000, png.length);
    const csv = seriesCSV(n.targets.filter((t) => n.series[t.id]).map((t) => ({ node: n.name, target: t.name, series: n.series[t.id] })));
    check("csvExport", csv.split("\n").length > 2, csv.split("\n").slice(0, 2));
    click('[data-act="tab"][data-v="incidents"]'); await sleep(800);
    check("incidentsTab", document.querySelector(".view .inclist, .view .empty") != null, text(".view .ntab").join(" ").slice(0, 120));
    click('[data-act="tab"][data-v="diagnostics"]');
    await until(() => !!n.system && Object.keys(n.summaries).length > 0, 70000);
    model.changed(); await sleep(1200);
    check("diagSystem", text(".view .kv").length >= 7, text(".view .kv"));
    check("diagSLA", document.querySelectorAll(".view table.sla tr").length === n.targets.length + 1, text(".view table.sla tr").slice(0, 3));
    check("diagInterfaces", text(".view .panel").some((t) => t.startsWith("NETWORK PATHS")), text(".view .panel header"));
    click('[data-act="tab"][data-v="control"]'); await sleep(1500);
    const inputs = [...document.querySelectorAll<HTMLInputElement>(".view input.field")].map((i) => `${i.dataset.path}=${i.value}`);
    check("controlInputs", inputs.some((i) => i.startsWith("intervalMs=")) && inputs.some((i) => i === "retentionRollupDays=60"), inputs.slice(0, 12));
    check("controlClients", text(".view .panel").some((t) => t.includes("desktop") || t.includes("PAIRED CONTROLLERS")), text(".view .panel header"));
    // 5. global pages
    model.go({ kind: "incidents" }); await sleep(600);
    check("incidentsPage", text(".pagehead").join("").includes("INCIDENT TIMELINE"));
    model.go({ kind: "settings" }); await sleep(800);
    const st = text(".view").join(" ");
    check("settingsAbout", st.includes("Developed by Wasim Osman") && st.includes("github.com/wasim-osman/InternetMonitor") && st.includes("MIT"), st.slice(0, 400));
    check("settingsToggles", document.querySelectorAll('.view input[type="checkbox"]').length === 4);
    // 6. updater check against the real GitHub API
    try { const u = await api.updateCheck(); R.updateCheck = u; } catch (e) { R.updateCheck = "error: " + errText(e); }
    // 7. tray + notification commands
    try { await api.trayUpdate("degraded", "selftest", ["line 1"]); R.tray = "ok"; } catch (e) { errors.push("tray"); R.tray = errText(e); }
  } catch (e) {
    errors.push("exception: " + errText(e));
  }
  R.errors = errors;
  R.pass = errors.length === 0;
  await api.selftestReport(JSON.stringify(R));
}
