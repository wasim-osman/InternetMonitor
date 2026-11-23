// Setup wizard: Discover → Connect → Targets → Review & apply. Mirrors WizardModel/WizardView.swift.
import { Agent, api, errText, onEvent } from "../api";
import { confirmDialog, delegate, esc, icons, patch, spinner } from "../dom";
import { C, endpointLabel, ipKey, isTailscaleIP, kindLabel, latencyColor, ms } from "../fmt";
import { model, type NodeState } from "../state";
import type { Candidate, ProbeKind } from "../types";
import {
  buildRows, cellKey, customTarget, newConn, parseHostPort, setColumn, setRow, targetsFor, toggleCell,
  type Conn, type Section, type TargetRow, type WCandidate,
} from "../wizardLogic";

const STEPS = ["Discover", "Connect", "Targets", "Review"];
const TITLES = ["Choose machines to monitor", "Connect to the agents", "Choose what each node pings", "Review & apply"];

export class Wizard {
  el: HTMLElement;
  step = 0;
  candidates: WCandidate[] = [];
  scanning = false;
  progress = "";
  selected = new Set<string>();
  conns: Record<string, Conn> = {};
  rows: TargetRow[] = [];
  grid = new Set<string>();
  applyResults: Record<string, string> = {};
  applying = false;
  done = false;
  manual = "";
  manualError = "";
  removeResults: Record<string, string> = {};
  removingIPs = new Set<string>();
  selfIPs = new Set<string>();
  custom = { kind: "icmp" as ProbeKind, name: "", host: "", port: "" };
  /** manually added or saved candidates (survive rescans) */
  pinned = new Set<string>();
  private unlisten: (() => void)[] = [];
  private clientName = "Internet Monitor desktop";

  constructor(public editing: boolean, private onClose: () => void) {
    this.el = document.createElement("div");
    this.el.className = "wiz-back";
    this.el.innerHTML = `<div class="wizard"></div>`;
    delegate(this.el, () => this.handlers());
    api.localIps().then((ips) => ips.forEach((i) => this.selfIPs.add(i))).catch(() => {});
    api.clientName().then((n) => (this.clientName = n)).catch(() => {});
    onEvent<string>("discovery:progress", (p) => { this.progress = p; this.render(); }).then((u) => this.unlisten.push(u));
    onEvent<Candidate[]>("discovery:update", (l) => { this.mergeScan(l); this.render(); }).then((u) => this.unlisten.push(u));
    onEvent<{ host: string; line: string }>("ssh:log", (e) => { if (this.conns[e.host]) { this.conns[e.host].log.push(e.line); this.render(); } }).then((u) => this.unlisten.push(u));
    if (editing) {
      for (const n of model.nodes) {
        const c: WCandidate = { ip: n.saved.host, port: n.saved.port, name: n.name, network: "Saved", isTailscale: isTailscaleIP(n.saved.host), agent: n.identity ?? null, extraIps: [] };
        this.candidates.push(c); this.selected.add(c.ip); this.pinned.add(c.ip);
        this.conns[c.ip] = { ...newConn(), phase: "ready", identity: n.identity, token: n.saved.token, sshUser: n.saved.sshUser ?? "", recognised: true, config: n.config };
      }
      this.buildRows();
      this.step = 2;
    } else {
      this.scan();
    }
    this.render();
  }

  close() { this.unlisten.forEach((u) => u()); this.el.remove(); this.onClose(); }

  // ---------- step 1: discover ----------
  async scan() {
    if (this.scanning) return;
    this.scanning = true; this.progress = "Starting…"; this.render();
    try { this.mergeScan(await api.discoverStart()); } catch (e) { this.progress = errText(e); }
    this.scanning = false;
    this.render();
  }

  /** Scan events carry the complete current list (the final one de-duplicated by nodeId), so they
   *  replace earlier scan results; manually added / saved entries are kept unless the scan covers them. */
  mergeScan(list: Candidate[]) {
    const covered = new Set(list.flatMap((c) => [c.ip, ...(c.extraIps ?? [])]));
    const keep = this.candidates.filter((c) => this.pinned.has(c.ip) && !covered.has(c.ip));
    for (const ip of [...this.selected]) if (covered.has(ip) && !list.some((x) => x.ip === ip)) this.remap(ip, list);
    this.candidates = [...list.map((c) => ({ ...this.candidates.find((o) => o.ip === c.ip), ...c })), ...keep].sort((a, b) => ipKey(a.ip) - ipKey(b.ip));
  }

  /** A selected address that was merged into another candidate's extraIps → select the primary instead. */
  private remap(ip: string, list: Candidate[]) {
    if (!this.selected.has(ip)) return;
    const p = list.find((c) => c.extraIps?.includes(ip));
    this.selected.delete(ip);
    if (p) this.selected.add(p.ip);
  }

  merge(list: WCandidate[]) {
    const by = new Map(this.candidates.map((c) => [c.ip, c]));
    for (const c of list) { by.set(c.ip, { ...by.get(c.ip), ...c }); this.pinned.add(c.ip); }
    this.candidates = [...by.values()].sort((a, b) => ipKey(a.ip) - ipKey(b.ip));
  }

  async addManual() {
    const raw = this.manual.trim();
    if (!raw) return;
    const { host, port } = parseHostPort(raw);
    this.manualError = "";
    const loop = host === "localhost" || host.startsWith("127.") || this.selfIPs.has(host);
    if (loop && !model.info.selftest) { this.manualError = "That is this machine — only other machines can be monitored."; this.render(); return; }
    this.manualError = "Checking…"; this.render();
    try {
      let c: WCandidate;
      if (port !== 7878 || loop) {
        const id = await api.probeAgent(host, port, 2500);
        if (!id) throw new Error(`No agent answered on ${host}:${port}.`);
        c = { ip: host, port, name: id.nodeName || id.hostname || host, network: "Manual", isTailscale: isTailscaleIP(host), agent: id, extraIps: [], os: id.os };
      } else {
        c = await api.probeHost(host);
      }
      this.merge([c]); this.selected.add(c.ip); this.manual = ""; this.manualError = "";
    } catch (e) { this.manualError = errText(e); }
    this.render();
  }

  savedNode(c: WCandidate): NodeState | undefined {
    return model.nodes.find((n) => n.saved.host === c.ip || (c.agent != null && n.id === c.agent.nodeId));
  }
  canRemove(c: WCandidate) { return c.agent != null || this.savedNode(c) != null; }
  get removableSelected() { return this.candidates.filter((c) => this.selected.has(c.ip) && this.canRemove(c)); }

  async removeAgents(list: WCandidate[]) {
    const ok = await confirmDialog({
      title: `Remove the agent from ${list.map((c) => c.name).join(", ")}?`,
      message: "Over SSH this stops the service and deletes the binary, systemd unit, config, tokens and all stored history on those machines. Stored history cannot be recovered.",
      confirm: "UNINSTALL AGENT & DELETE ALL ITS DATA", danger: true,
    });
    if (!ok) return;
    for (const c of list) {
      this.removingIPs.add(c.ip); this.removeResults[c.ip] = "Removing…"; this.render();
      const saved = this.savedNode(c);
      const r = await api.removeAgent(c.ip, saved?.saved.sshUser ?? this.conns[c.ip]?.sshUser ?? null);
      this.removingIPs.delete(c.ip);
      if (r.error) { this.removeResults[c.ip] = "FAILED: " + r.error; this.render(); continue; }
      if (saved) model.remove(saved);
      const i = this.candidates.findIndex((x) => x.ip === c.ip);
      if (i >= 0) this.candidates[i] = { ...this.candidates[i], agent: null };
      delete this.conns[c.ip]; this.selected.delete(c.ip);
      this.removeResults[c.ip] = r.leftovers.length ? "Removed, leftovers: " + r.leftovers.join(", ") : "Agent removed — no files left on the node";
      this.render();
    }
  }

  // ---------- step 2: connect ----------
  beginConnect() {
    this.step = 1;
    for (const ip of this.selected) if (this.conns[ip]?.phase !== "ready") this.check(ip);
    this.render();
  }

  conn(ip: string): Conn { return (this.conns[ip] ??= newConn()); }
  cand(ip: string) { return this.candidates.find((c) => c.ip === ip); }
  port(ip: string) { return this.cand(ip)?.port ?? 7878; }
  log(ip: string, s: string) { this.conn(ip).log.push(s); this.render(); }

  async check(ip: string) {
    const c = this.conn(ip);
    c.phase = "checking"; c.message = ""; this.render();
    const cand = this.cand(ip);
    if (!cand) return;
    let ident = cand.agent ?? null;
    if (!ident) ident = await api.probeAgent(ip, this.port(ip), 2500).catch(() => null);
    c.identity = ident;
    if (!c.sshUser) c.sshUser = model.nodes.find((n) => n.saved.host === ip)?.saved.sshUser ?? "";
    if (!ident) { c.phase = "noAgent"; c.message = "No InternetMonitor agent found on this machine."; this.render(); return; }
    c.recognised = true;
    const saved = model.node(ident.nodeId);
    if (saved) {
      try {
        const cfg = await new Agent({ host: ip, port: this.port(ip), token: saved.saved.token }).config();
        Object.assign(c, { token: saved.saved.token, config: cfg, phase: "ready", message: "Already paired with this computer." });
        this.render(); return;
      } catch { /* token no longer valid → pair again */ }
    }
    c.phase = "needsCode";
    c.message = `Agent v${ident.version ?? "?"} found (${ident.hostname ?? ip}). Enter the pairing code shown by \`imonitord pair-code\` on that machine, or connect automatically over SSH.`;
    this.render();
    if (!model.info.selftest) await this.connectViaSSH(ip, true); // zero-typing path
  }

  async connectViaSSH(ip: string, silent = false) {
    const c = this.conn(ip);
    c.phase = "working"; c.log = ["Trying SSH…"]; this.render();
    const users = await api.sshGuessUsers(c.sshUser || null);
    const user = await api.sshProbeLogin(ip, users);
    if (!user) {
      c.phase = c.identity ? "needsCode" : "noAgent";
      if (!silent) { c.message = `SSH login failed for ${users.join(", ")}. Enter a user name and retry, or paste the pairing code.`; c.log.push(`No SSH login worked (tried ${users.join(", ")})`); }
      this.render(); return;
    }
    c.sshUser = user; this.log(ip, `SSH ok as ${user}`);
    if (!c.identity) { await this.installAgent(ip, user); return; }
    try { const code = await api.sshFetchPairCode(user, ip); this.log(ip, "Got pairing code"); await this.pair(ip, code); }
    catch (e) { c.phase = "needsCode"; c.message = `Could not fetch a code over SSH: ${errText(e)}`; this.render(); }
  }

  async installAgent(ip: string, user?: string) {
    const c = this.conn(ip);
    c.phase = "working"; this.render();
    const u = user ?? (c.sshUser || (await api.sshGuessUsers(null))[0] || "pi");
    c.sshUser = u;
    try { await api.sshInstallAgent(u, ip); }
    catch (e) { c.phase = "noAgent"; c.message = errText(e); this.render(); return; }
    await new Promise((r) => setTimeout(r, 1500));
    const ident = await api.probeAgent(ip, this.port(ip), 3000).catch(() => null);
    c.identity = ident;
    if (!ident) { c.phase = "noAgent"; c.message = "Installed, but the agent is not answering on port 7878 yet."; this.render(); return; }
    try { await this.pair(ip, await api.sshFetchPairCode(u, ip)); }
    catch (e) { c.phase = "needsCode"; c.message = errText(e); this.render(); }
  }

  async pair(ip: string, code: string) {
    const c = this.conn(ip);
    c.phase = "working"; this.render();
    try {
      const r = await new Agent({ host: ip, port: this.port(ip) }).pair(code.trim(), this.clientName);
      const cfg = await new Agent({ host: ip, port: this.port(ip), token: r.token }).config();
      Object.assign(c, { token: r.token, config: cfg, phase: "ready", message: "Paired." });
    } catch (e) { c.phase = "needsCode"; c.message = errText(e); }
    this.render();
  }

  get readyIPs() { return this.candidates.map((c) => c.ip).filter((ip) => this.selected.has(ip) && this.conns[ip]?.phase === "ready"); }

  // ---------- step 3/4 ----------
  buildRows() { const r = buildRows(this.candidates, this.selected, this.conns, this.editing); this.rows = r.rows; this.grid = r.grid; }
  count(ip: string) { return this.rows.filter((r) => this.grid.has(cellKey(r.id, ip))).length; }

  async apply(): Promise<boolean> {
    this.applying = true; this.applyResults = {}; this.render();
    let ok = true;
    for (const ip of this.readyIPs) {
      const c = this.conns[ip];
      if (!c?.token || !c.identity) continue;
      const a = new Agent({ host: ip, port: this.port(ip), token: c.token });
      try {
        const cfg = await a.config();
        cfg.targets = targetsFor(this.rows, this.grid, ip, cfg.targets);
        if ((cfg.retentionRollupDays ?? 0) < 60) cfg.retentionRollupDays = 60;
        await a.putConfig(cfg);
        model.add({ nodeId: c.identity.nodeId, name: c.identity.nodeName || c.identity.hostname || ip, host: ip, port: this.port(ip), token: c.token, sshUser: c.sshUser || null, addedAt: new Date().toISOString() });
        this.applyResults[ip] = `OK — ${cfg.targets.length} targets`;
      } catch (e) { ok = false; this.applyResults[ip] = `Failed: ${errText(e)}`; }
      this.render();
    }
    this.applying = false; this.done = ok; this.render();
    return ok;
  }

  // ---------- rendering ----------
  render() {
    const box = this.el.querySelector<HTMLElement>(".wizard")!;
    const head = `<div class="wizhead row">
      <div class="col gap2"><span class="mono10 bold amber track2">${this.editing ? "EDIT MONITORING" : "SETUP"}</span><span class="h20">${esc(TITLES[this.step])}</span></div>
      <span class="grow"></span>
      <div class="row gap6">${STEPS.map((s, i) => `<span class="wstep ${i === this.step ? "on" : i < this.step ? "past" : ""}"><b>${String(i + 1).padStart(2, "0")}</b> ${s.toUpperCase()}</span>`).join("")}</div>
    </div>`;
    const body = [() => this.scanStep(), () => this.connectStep(), () => this.targetsStep(), () => this.reviewStep()][this.step]();
    patch(box, `${head}<div class="wizbody scroll" data-scroll="wiz-${this.step}">${body}</div>${this.footer()}`);
  }

  footer(): string {
    const cancel = model.nodes.length || this.editing ? `<button class="btn" data-act="w-cancel">CANCEL</button>` : "";
    const back = this.step > 0 ? `<button class="btn" data-act="w-back">BACK</button>` : "";
    let next = "";
    if (this.step === 0) next = `<button class="btn primary" data-act="w-connect" ${this.selected.size ? "" : "disabled"}>CONNECT TO ${this.selected.size} NODE${this.selected.size === 1 ? "" : "S"} →</button>`;
    if (this.step === 1) next = `<button class="btn primary" data-act="w-targets" ${this.readyIPs.length ? "" : "disabled"} title="Continues with the nodes that are connected; the rest are skipped">CHOOSE TARGETS →</button>`;
    if (this.step === 2) next = `<button class="btn primary" data-act="w-review">REVIEW →</button>`;
    return `<div class="wizfoot row gap8">${cancel}<span class="grow"></span>${back}${next}</div>`;
  }

  scanStep(): string {
    const rows = this.candidates.map((c) => {
      const on = this.selected.has(c.ip);
      const st = this.removeResults[c.ip];
      const stCol = st?.startsWith("FAILED") ? C.red : st === "Removing…" ? C.amber : C.green;
      const agent = c.agent ? `<span class="mono10 bold cyan">AGENT v${esc(c.agent.version ?? "?")}</span><span class="mono9 dim">${c.agent.paired ? "PAIRED" : "UNPAIRED"}</span>` : `<span class="faint">—</span>`;
      const removeBtn = this.removingIPs.has(c.ip) ? spinner() : this.canRemove(c) ? `<button class="linkbtn red" data-act="w-remove-one" data-ip="${esc(c.ip)}">REMOVE</button>` : "";
      const extra = c.extraIps?.length ? `<div class="mono9 faint">also ${esc(c.extraIps.join(", "))}</div>` : "";
      return `<div class="cand ${on ? "on" : ""}" data-act="w-toggle" data-ip="${esc(c.ip)}">
        <span class="chk">${on ? icons.check(13) : ""}</span>
        <span class="w190 row gap6 ell"><span style="color:${c.agent ? C.cyan : C.dim}">${c.agent ? icons.cpu(14) : icons.desktop(14)}</span><span class="ell">${esc(c.name)}</span></span>
        <span class="w130 dim"><div>${esc(c.ip)}${c.port && c.port !== 7878 ? ":" + c.port : ""}</div>${extra}</span>
        <span class="w150 mono10" style="color:${c.isTailscale ? C.cyan : C.dim}">${esc(c.network.toUpperCase())}</span>
        <span class="w70 r" style="color:${latencyColor(c.pingMs)}">${c.pingMs != null ? ms(c.pingMs) + " ms" : "—"}</span>
        <span class="grow row gap8 pl16">${agent}${st ? `<span class="mono10" style="color:${stCol}">${esc(st)}</span>` : ""}<span class="grow"></span>${removeBtn}</span>
      </div>`;
    }).join("");
    const empty = `<div class="center col gap8" style="min-height:220px"><span class="dim">${this.scanning ? "Looking for machines on your networks…" : "No other machines found."}</span>
      <span class="small faint">Scans every active interface (Wi-Fi, Ethernet, Tailscale…) for hosts that answer ping or run the agent.</span></div>`;
    return `<div class="col gap12 pad">
      <div class="row gap10">${this.scanning ? spinner() : ""}<span class="mono11 dim">${esc(this.progress.toUpperCase())}</span><span class="grow"></span>
        <span class="mono10 bold faint track">THIS COMPUTER IS EXCLUDED</span>
        <button class="btn danger" data-act="w-remove-sel" ${this.removableSelected.length && !this.removingIPs.size ? "" : "disabled"}>REMOVE AGENT FROM SELECTED (${this.removableSelected.length})</button>
        <button class="btn" data-act="w-scan" ${this.scanning ? "disabled" : ""}>${this.scanning ? "SCANNING…" : "RESCAN"}</button></div>
      <section class="panel"><div class="pbody">${this.candidates.length ? `<div class="candhead"><span class="chk"></span><span class="w190">HOST</span><span class="w130">ADDRESS</span><span class="w150">NETWORK</span><span class="w70 r">PING</span><span class="grow pl16">AGENT</span></div><div class="candlist">${rows}</div>` : empty}</div></section>
      <div class="row gap8"><span class="mono10 bold dim track">ADD BY ADDRESS</span>
        <input class="field w240" data-k="manual" data-in="w-manual" data-enter="w-add" placeholder="IP or hostname[:port]" value="${esc(this.manual)}">
        <button class="btn" data-act="w-add">ADD</button>${this.manualError ? `<span class="small ${this.manualError === "Checking…" ? "dim" : "red"}">${esc(this.manualError)}</span>` : ""}</div>
    </div>`;
  }

  connectStep(): string {
    const cards = this.candidates.filter((c) => this.selected.has(c.ip)).map((c) => {
      const k = this.conn(c.ip);
      const [ic, col] = k.phase === "ready" ? [icons.ok(22), C.green] : k.phase === "needsCode" ? [icons.key(22), C.amber] : k.phase === "noAgent" || k.phase === "failed" ? [icons.warn(22), C.red] : [icons.dots(22), C.dim];
      const id = k.identity;
      const sshField = `<span class="mono9 bold faint">SSH USER</span><input class="field" style="width:110px" data-k="user-${esc(c.ip)}" data-in="w-user" data-ip="${esc(c.ip)}" placeholder="user" value="${esc(k.sshUser)}">`;
      let actions = "";
      if (k.phase === "checking" || k.phase === "working") actions = `<div class="row gap8">${spinner()}<span class="mono11 dim">${esc(k.log[k.log.length - 1] ?? "Working…")}</span></div>`;
      else if (k.phase === "ready") actions = `<div class="row gap6 green small">${icons.check(14)} Connected — controlled from this computer</div>`;
      else if (k.phase === "needsCode") actions = `<div class="row gap8 wrap">
          <input class="field" style="width:140px" data-k="code-${esc(c.ip)}" data-in="w-code" data-enter="w-pair" data-ip="${esc(c.ip)}" placeholder="6-digit code" value="${esc(k.code)}">
          <button class="btn primary" data-act="w-pair" data-ip="${esc(c.ip)}" ${k.code.trim().length < 6 ? "disabled" : ""}>PAIR</button>
          <span class="vsep"></span>${sshField}<button class="btn" data-act="w-ssh" data-ip="${esc(c.ip)}">CONNECT VIA SSH</button></div>`;
      else actions = `<div class="row gap8 wrap">${sshField}<button class="btn primary" data-act="w-install" data-ip="${esc(c.ip)}">INSTALL AGENT VIA SSH</button><button class="btn" data-act="w-recheck" data-ip="${esc(c.ip)}">RECHECK</button></div>`;
      return `<section class="panel"><div class="pbody col gap10">
        <div class="row gap10"><span style="color:${col}">${ic}</span>
          <div class="col gap2 grow"><b>${esc(c.name)}</b><span class="mono11 dim">${esc(c.ip)}${id ? ` · ${esc(id.os ?? "")}/${esc(id.arch ?? "")} · agent v${esc(id.version ?? "?")}` : ""}</span></div>
          ${k.recognised && k.phase !== "ready" ? `<span class="mono9 bold cyan track">EXISTING AGENT RECOGNISED</span>` : ""}</div>
        ${k.message ? `<div class="small" style="color:${k.phase === "failed" ? C.red : C.dim}">${esc(k.message)}</div>` : ""}
        ${actions}
        ${k.log.length && k.phase !== "ready" ? `<pre class="log">${esc(k.log.join("\n"))}</pre>` : ""}
      </div></section>`;
    }).join("");
    return `<div class="col gap12 pad">${cards}<div class="small faint">Pairing codes come from the agent itself (<span class="mono10">imonitord pair-code</span>). Over SSH this happens automatically using your existing keys; no passwords are asked for or stored.</div></div>`;
  }

  targetsStep(): string {
    const nodes = this.candidates.filter((c) => this.readyIPs.includes(c.ip));
    const head = `<div class="tgrid-row thead"><span class="grow">TARGET</span>${nodes.map((n) => `<span class="tcol col center">
      <b class="mono10 ell" style="max-width:116px">${esc(this.conns[n.ip]?.identity?.nodeName || this.conns[n.ip]?.identity?.hostname || n.name)}</b>
      <span class="mono9 faint">${this.count(n.ip)} targets</span>
      <span class="row gap6"><a class="linkbtn" data-act="w-col" data-ip="${esc(n.ip)}" data-on="1">all</a><a class="linkbtn" data-act="w-col" data-ip="${esc(n.ip)}" data-on="0">none</a></span></span>`).join("")}<span class="w60"></span></div>`;
    const sections: Section[] = ["Nodes", "Baseline", "Existing", "Custom"];
    const body = sections.map((sec) => {
      const rs = this.rows.filter((r) => r.section === sec);
      if (!rs.length) return "";
      return `<div class="mono9 bold amber track2 sechdr">${sec.toUpperCase()}</div>` + rs.map((r) => `<div class="tgrid-row">
        <span class="grow row gap8"><span class="ktag">${kindLabel(r.template.kind)}</span>
          <span class="col"><span class="small bold ell">${esc(r.template.name)}</span><span class="mono10 faint ell">${esc(endpointLabel(r.template))}</span></span>
          <span class="grow"></span><a class="linkbtn" data-act="w-row" data-id="${esc(r.id)}" data-on="1">all</a><a class="linkbtn" data-act="w-row" data-id="${esc(r.id)}" data-on="0">none</a></span>
        ${nodes.map((n) => {
          const self = r.forNodeIP === n.ip;
          const on = this.grid.has(cellKey(r.id, n.ip));
          return `<span class="tcol center">${self ? `<span class="faint">—</span>` : `<button class="cell ${on ? "on" : ""}" data-act="w-cell" data-id="${esc(r.id)}" data-ip="${esc(n.ip)}">${on ? icons.check(12) : ""}</button>`}</span>`;
        }).join("")}
        <span class="w60 center">${sec === "Custom" || sec === "Existing" ? `<button class="iconbtn" data-act="w-delrow" data-id="${esc(r.id)}" title="Remove">${icons.trash(14)}</button>` : ""}</span>
      </div>`).join("");
    }).join("");
    const k = this.custom;
    const ph = k.kind === "http" ? "https://example.com" : k.kind === "dns" ? "domain to resolve" : "host or IP";
    return `<div class="col gap12 pad">
      <section class="panel"><div class="pbody"><div class="tgrid">${head}${body}</div></div></section>
      <section class="panel"><header><span>ADD CUSTOM ADDRESS</span></header><div class="pbody row gap8">
        <div class="seg">${(["icmp", "tcp", "http", "dns"] as ProbeKind[]).map((x) => `<button class="${k.kind === x ? "on" : ""}" data-act="w-kind" data-v="${x}">${kindLabel(x)}</button>`).join("")}</div>
        <input class="field" style="width:150px" data-k="c-name" data-in="w-cname" placeholder="Label (optional)" value="${esc(k.name)}">
        <input class="field grow" data-k="c-host" data-in="w-chost" data-enter="w-cadd" placeholder="${ph}" value="${esc(k.host)}">
        ${k.kind === "tcp" ? `<input class="field" style="width:70px" data-k="c-port" data-in="w-cport" placeholder="port" value="${esc(k.port)}">` : ""}
        <button class="btn" data-act="w-cadd" ${k.host.trim() ? "" : "disabled"}>ADD</button>
      </div></section></div>`;
  }

  reviewStep(): string {
    const cards = this.candidates.filter((c) => this.readyIPs.includes(c.ip)).map((n) => {
      const r = this.applyResults[n.ip];
      const names = this.rows.filter((x) => this.grid.has(cellKey(x.id, n.ip))).map((x) => x.template.name).join("  ·  ");
      return `<section class="panel"><div class="pbody col gap6"><div class="row"><div class="col gap2 grow"><b>${esc(this.conns[n.ip]?.identity?.nodeName || this.conns[n.ip]?.identity?.hostname || n.name)}</b>
        <span class="mono11 dim">${esc(n.ip)} → ${this.count(n.ip)} targets</span></div>${r ? `<span class="mono11" style="color:${r.startsWith("OK") ? C.green : C.red}">${esc(r)}</span>` : ""}</div>
        <div class="mono10 faint">${esc(names)}</div></div></section>`;
    }).join("");
    return `<div class="col gap14 pad">${cards}
      <div class="small faint">Applying writes the target list to each agent's config; probing starts immediately and history is stored on the node (60 days).</div>
      <div class="row"><span class="grow"></span><button class="btn primary" data-act="w-apply" ${this.applying ? "disabled" : ""}>${this.applying ? "APPLYING…" : this.done ? "OPEN DASHBOARD" : "APPLY & START MONITORING"}</button></div></div>`;
  }

  handlers() {
    const ip = (e: HTMLElement) => e.dataset.ip!;
    return {
      "w-cancel": () => this.close(),
      "w-back": () => { this.step = Math.max(0, this.step - 1); this.render(); },
      "w-connect": () => this.beginConnect(),
      "w-targets": () => { this.buildRows(); this.step = 2; this.render(); },
      "w-review": () => { this.step = 3; this.render(); },
      "w-scan": () => this.scan(),
      "w-toggle": (e: HTMLElement) => { const i = ip(e); if (this.selected.has(i)) this.selected.delete(i); else this.selected.add(i); this.render(); },
      "w-remove-one": (e: HTMLElement) => { const c = this.cand(ip(e)); if (c) this.removeAgents([c]); },
      "w-remove-sel": () => this.removeAgents(this.removableSelected),
      "w-manual": (e: HTMLElement) => { this.manual = (e as HTMLInputElement).value; },
      "w-add": () => this.addManual(),
      "w-user": (e: HTMLElement) => { this.conn(ip(e)).sshUser = (e as HTMLInputElement).value.trim(); },
      "w-code": (e: HTMLElement) => {
        this.conn(ip(e)).code = (e as HTMLInputElement).value;
        const b = this.el.querySelector<HTMLButtonElement>(`[data-act="w-pair"][data-ip="${CSS.escape(ip(e))}"]`);
        if (b) b.disabled = this.conn(ip(e)).code.trim().length < 6;
      },
      "w-pair": (e: HTMLElement) => { const k = this.conn(ip(e)); if (k.code.trim().length >= 6) this.pair(ip(e), k.code); },
      "w-ssh": (e: HTMLElement) => this.connectViaSSH(ip(e)),
      "w-install": (e: HTMLElement) => this.installAgent(ip(e)),
      "w-recheck": (e: HTMLElement) => this.check(ip(e)),
      "w-col": (e: HTMLElement) => { setColumn(this.grid, this.rows, ip(e), e.dataset.on === "1"); this.render(); },
      "w-row": (e: HTMLElement) => { const r = this.rows.find((x) => x.id === e.dataset.id); if (r) setRow(this.grid, r, this.readyIPs, e.dataset.on === "1"); this.render(); },
      "w-cell": (e: HTMLElement) => { const r = this.rows.find((x) => x.id === e.dataset.id); if (r) toggleCell(this.grid, r, ip(e)); this.render(); },
      "w-delrow": (e: HTMLElement) => { const id = e.dataset.id!; this.rows = this.rows.filter((r) => r.id !== id); this.grid = new Set([...this.grid].filter((k) => !k.startsWith(id + "@@"))); this.render(); },
      "w-kind": (e: HTMLElement) => { this.custom.kind = e.dataset.v as ProbeKind; this.render(); },
      "w-cname": (e: HTMLElement) => { this.custom.name = (e as HTMLInputElement).value; },
      "w-chost": (e: HTMLElement) => {
        const had = !!this.custom.host.trim();
        this.custom.host = (e as HTMLInputElement).value;
        if (had !== !!this.custom.host.trim()) this.render();
      },
      "w-cport": (e: HTMLElement) => { this.custom.port = (e as HTMLInputElement).value; },
      "w-cadd": () => {
        const k = this.custom;
        const t = customTarget(k.kind, k.name, k.host, k.port ? parseInt(k.port, 10) || null : null);
        if (!t) return;
        const row: TargetRow = { id: t.id, template: t, section: "Custom" };
        this.rows.push(row); setRow(this.grid, row, this.readyIPs, true);
        this.custom = { kind: k.kind, name: "", host: "", port: "" };
        this.render();
      },
      "w-apply": async () => {
        if (this.done) { this.close(); return; }
        const ok = await this.apply();
        if (ok) setTimeout(() => this.close(), 600);
      },
    };
  }
}
