// Services runtime: official status polling, desktop reachability probes, APPLY to nodes.
// Pure rules live in services.ts; this file only schedules and stores.
import { api, errText } from "./api";
import { model, type NodeState } from "./state";
import {
  allServices, defaultServiceSettings, isBad, newReach, NO_FEED, normalizeSettings, officialIntervalSec, officialNotes,
  PROBE_INTERVAL_SEC, reachStep, reconcileTargets, selectionKey, targetId, type Official, type Reach, type ServiceDef,
  type ServiceSettings,
} from "./services";

export interface OfficialEntry { data: Official; at: number }
export interface ApplyResult { ok: boolean; msg: string }

const now = () => Date.now() / 1000;

class ServicesModel {
  st: ServiceSettings = defaultServiceSettings();
  official = new Map<string, OfficialEntry>();
  reach = new Map<string, Reach>();
  applying = false;
  applyResults: Record<string, ApplyResult> = {};
  private lastKnown = new Map<string, Official>(); // last non-unknown observation (notification baseline)
  private fetching = new Set<string>();
  private probing = new Set<string>();
  private started = false;

  init() {
    this.st = normalizeSettings(model.settings.services);
    if (!this.st.applied) this.st.applied = selectionKey(defaultServiceSettings(), []);
  }

  get defs(): ServiceDef[] { return allServices(this.st.custom); }
  def(id: string) { return this.defs.find((d) => d.id === id); }
  get selected(): ServiceDef[] {
    const sel = new Set(this.st.selected);
    return this.defs.filter((d) => sel.has(d.id));
  }
  isSelected(id: string) { return this.st.selected.includes(id); }
  nodeOn(id: string) { return this.st.nodes.includes(id); }

  /** Selected services whose official state is degraded / outage (sidebar badge). */
  get badCount() { return this.selected.filter((d) => isBad(this.official.get(d.id)?.data.state)).length; }
  /** What APPLY would write, ignoring toggles of nodes that no longer exist. */
  private key() {
    const ids = new Set(model.nodes.map((n) => n.id));
    return selectionKey({ ...this.st, nodes: this.st.nodes.filter((x) => ids.has(x)) }, this.defs);
  }
  get applyNeeded() { return this.key() !== this.st.applied; }

  private save() {
    model.saveSettings({ services: structuredClone(this.st) }).catch(console.error);
    model.changed();
  }

  toggleService(id: string, on?: boolean) {
    const has = this.isSelected(id);
    const want = on ?? !has;
    if (want === has) return;
    this.st.selected = want ? [...this.st.selected, id] : this.st.selected.filter((x) => x !== id);
    if (!want) { this.lastKnown.delete(id); this.reach.delete(id); }
    this.save();
    if (want) { const d = this.def(id); if (d) { this.fetchOfficial(d); if (this.st.desktop) this.probeOne(d); } }
  }

  toggleNode(id: string, on?: boolean) {
    const has = this.nodeOn(id);
    const want = on ?? !has;
    if (want === has) return;
    this.st.nodes = want ? [...this.st.nodes, id] : this.st.nodes.filter((x) => x !== id);
    this.save();
  }

  setDesktop(on: boolean) {
    if (this.st.desktop === on) return;
    this.st.desktop = on;
    if (!on) this.reach.clear();
    this.save();
    if (on) this.probeAll();
  }

  addCustom(d: ServiceDef) {
    this.st.custom = [...this.st.custom, d];
    this.st.selected = [...this.st.selected, d.id];
    this.save();
    this.fetchOfficial(d);
    if (this.st.desktop) this.probeOne(d);
  }

  removeCustom(id: string) {
    this.st.custom = this.st.custom.filter((c) => c.id !== id);
    this.st.selected = this.st.selected.filter((x) => x !== id);
    this.official.delete(id); this.lastKnown.delete(id); this.reach.delete(id);
    this.save();
  }

  start() {
    if (this.started) return;
    this.started = true;
    this.pollOfficial();
    setInterval(() => this.pollOfficial(), 5000);
    this.probeAll();
    setInterval(() => this.probeAll(), PROBE_INTERVAL_SEC * 1000);
  }

  // ---------- official status ----------

  private pollOfficial() {
    for (const d of this.selected) {
      const e = this.official.get(d.id);
      if (!e || now() - e.at >= officialIntervalSec(d.adapter)) this.fetchOfficial(d);
    }
  }

  async fetchOfficial(d: ServiceDef) {
    if (this.fetching.has(d.id)) return;
    if (d.adapter === "none") {
      this.official.set(d.id, { data: { state: "unknown", summary: NO_FEED, incidents: [], components: [] }, at: now() });
      model.changed();
      return;
    }
    this.fetching.add(d.id);
    let data: Official;
    try { data = await api.serviceStatus(d.adapter, d.statusUrl); } catch (e) { data = { state: "unknown", summary: "Status feed unavailable: " + errText(e), incidents: [], components: [], error: errText(e) }; }
    this.fetching.delete(d.id);
    if (!this.isSelected(d.id)) return;
    this.official.set(d.id, { data, at: now() });
    if (data.state !== "unknown") {
      for (const n of officialNotes(d.name, this.lastKnown.get(d.id), data)) model.notify(n.title, n.body);
      this.lastKnown.set(d.id, data);
    }
    model.changed();
  }

  // ---------- desktop reachability ----------

  private probeAll() {
    if (!this.st.desktop) return;
    for (const d of this.selected) this.probeOne(d);
  }

  async probeOne(d: ServiceDef) {
    if (!this.st.desktop || this.probing.has(d.id)) return;
    this.probing.add(d.id);
    try {
      const r = await api.serviceProbe(d.probeUrl).catch((e) => ({ ok: false, ms: null, status: null, error: errText(e) }));
      if (!this.st.desktop || !this.isSelected(d.id)) return;
      let st = this.reach.get(d.id);
      if (!st) { st = newReach(); this.reach.set(d.id, st); }
      const note = reachStep(st, d.name, r.ok, r.ms ?? null, r.error ?? null, now());
      if (note) model.notify(note.title, note.body);
      model.changed();
    } finally { this.probing.delete(d.id); }
  }

  // ---------- APPLY to nodes ----------

  /** Live reachability of `svc-<id>` on a node; `applied` = target present in its config. */
  nodeReach(n: NodeState, svcId: string) {
    const tid = targetId(svcId);
    return { applied: !!n.config?.targets.some((t) => t.id === tid), live: n.live[tid] };
  }

  async apply(): Promise<boolean> {
    if (this.applying) return false;
    this.applying = true;
    this.applyResults = {};
    model.changed();
    const key = this.key();
    const sel = this.selected;
    let ok = true;
    await Promise.all(model.nodes.map(async (n) => {
      const desired = this.nodeOn(n.id) ? sel : [];
      try {
        const cfg = await n.agent.config();
        const r = reconcileTargets(cfg.targets ?? [], desired);
        if (r.changed) {
          cfg.targets = r.targets;
          n.config = await n.agent.putConfig(cfg);
          this.applyResults[n.id] = { ok: true, msg: `OK — ${desired.length} service target${desired.length === 1 ? "" : "s"} (+${r.added} / −${r.removed})` };
        } else {
          n.config = cfg;
          this.applyResults[n.id] = { ok: true, msg: `OK — ${desired.length} service target${desired.length === 1 ? "" : "s"}, no change` };
        }
      } catch (e) {
        ok = false;
        this.applyResults[n.id] = { ok: false, msg: "Failed: " + errText(e) };
      }
      model.changed();
    }));
    if (ok) { this.st.applied = key; this.save(); }
    this.applying = false;
    model.changed();
    return ok;
  }
}

export const services = new ServicesModel();
