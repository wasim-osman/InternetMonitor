# InternetMonitor agent API (v1)

`imonitord` is a single static Go binary (stdlib only) that runs on each monitored node
(Raspberry Pi etc.). The macOS app is a dashboard + controller; **all probing happens on the agents**
and all history is stored on the agents. A fresh Mac install can therefore recognise, pair with and
control an agent that is already running and immediately see its history.

* Default listen: `:7878` (TCP, HTTP/1.1, JSON, UTF-8). Flag `--listen`, `--state-dir` (default `/var/lib/imonitor`).
* JSON keys are **camelCase**. Times are unix **seconds** (integers) unless a name ends in `Ms`.
* `null`/absent optional fields are equivalent. Unknown fields must be ignored by both sides.
* Auth: `Authorization: Bearer <token>` on everything except endpoints marked **open**.
* Errors: non-2xx with body `{"error":"message"}`. 401 bad/missing token, 429 rate limited, 400 validation.
* `apiVersion` is 1. Agent `version` is semver (app embeds the expected version).

## Recognition & pairing

### GET /v1/identity — open
```json
{"service":"internetmonitor-agent","apiVersion":1,"version":"1.0.0","nodeId":"<uuid, stable, persisted>",
 "hostname":"pi-a","nodeName":"Pi A","os":"linux","arch":"arm64",
 "uptimeSec":1234,"startedAt":1700000000,"paired":true}
```
`paired` = at least one client token exists. The app recognises an agent by `service == "internetmonitor-agent"`.

### POST /v1/pair — open, rate limited (5 attempts / minute / source IP)
Body `{"code":"123456","clientName":"Wasim's MacBook Pro"}` → `{"token":"<64 hex>","nodeId":"...","clientId":"..."}`.
Codes are 6 digits, single use, valid 10 minutes. Wrong/expired code → 401.

### POST /v1/local/pair-code — open but **loopback source only** (403 otherwise)
Creates a code → `{"code":"123456","expiresAt":1700000600}`. Used by CLI `imonitord pair-code` (which just calls this on 127.0.0.1)
and by the app over SSH (`ssh pi imonitord pair-code --json`).

### POST /v1/pair-code — auth
Same response as above, so a paired client can authorise another device.

### GET /v1/clients — auth → `[{"id","name","createdAt","lastSeen"}]`; DELETE /v1/clients/{id} — auth (cannot be undone).

## Config (control)

### GET /v1/config , PUT /v1/config — auth
PUT replaces the whole document, validates, persists atomically, applies live (no restart), returns the saved config.
```json
{
 "nodeName":"Pi A",
 "intervalMs":2000,            // default probe interval, min 500
 "timeoutMs":2000,             // per-probe timeout, min 200, must be <= intervalMs*5
 "retentionRawHours":6,        // raw samples kept
 "retentionRollupDays":60,     // 1-minute rollups kept
 "metricsPublic":false,        // /metrics without auth
 "targets":[Target],
 "rules":{"downAfterFailures":3,"degradedLatencyMs":150,"degradedForSec":60,"webhookUrl":""},
 "speedtest":{"enabled":false,"intervalMin":60,"url":"https://speed.cloudflare.com/__down?bytes=25000000"}
}
```
`Target`:
```json
{"id":"t-abc123","name":"Pi B","kind":"icmp","host":"100.100.0.2","port":null,"url":null,"resolver":null,
 "intervalMs":null,"enabled":true,"group":"node","nodeRef":"<nodeId of the node being pinged, or null>"}
```
* `kind`: `icmp` | `tcp` (host+port) | `http` (url; success = any response received, timing = time to first byte; status code recorded in `lastError` if >=500) | `dns` (host = name to resolve; optional `resolver` "ip" or "ip:port", default system resolver).
* `group`: free text used by the UI: `node`, `baseline`, `custom`.
* `host` may be the special value **`@gateway`**: resolved each cycle to the default-route gateway.
* `id` must be unique and stable (the app generates it). Samples are keyed by target id. Empty target list is valid.
* Default config on a fresh agent: no targets except baseline `@gateway`(icmp, group baseline), `1.1.1.1`(icmp), `8.8.8.8`(icmp).

## Live & history

### GET /v1/live — auth
```json
{"now":1700000000,"targets":[{"id":"t-abc123","state":"up","lastRttMs":12.3,"lastAt":1700000000,"lastError":null,
 "resolvedHost":"192.168.50.1","windowSec":60,"avgMs":12.0,"minMs":9.1,"maxMs":30.2,"p95Ms":22.0,"jitterMs":3.1,
 "lossPct":0.0,"samples":30,"pathHint":"direct"}]}
```
`state`: `up` | `degraded` (latency rule tripped) | `down` (>= downAfterFailures consecutive failures) | `unknown` (no samples yet / disabled).
`jitterMs` = mean absolute difference of consecutive successful RTTs in the window. `pathHint` only for targets that are Tailscale IPs (100.64.0.0/10): `direct`|`relay`|null.

### GET /v1/series?target=id1,id2&from=<unix>&to=<unix>&points=<N, default 300, max 1500> — auth
```json
{"from":..,"to":..,"series":[{"target":"id1","stepSec":60,"source":"raw","points":[{"t":1700000000,"n":30,"loss":0.0,"min":9.1,"avg":12.0,"max":30.2,"p95":22.0}]}]}
```
Server picks `stepSec` ≈ (to-from)/points rounded up to a multiple of the probe interval (raw) or to a multiple of 60 (rollup).
Uses raw samples when the whole range is inside raw retention and step < 60, otherwise 1-minute rollups (rollups are kept continuously
for the whole retention). `loss` is percent of probes in the bucket that failed. `min/avg/max/p95` omitted when every probe in the bucket failed.
Empty buckets are omitted. Points ascending by `t`.

### GET /v1/summary?target=id — auth (omit `target` for all targets)
```json
{"summaries":[{"target":"id1","windows":[{"label":"1h","seconds":3600,"uptimePct":99.9,"lossPct":0.2,"avgMs":12.1,"p95Ms":25,"minMs":8,"maxMs":90,"jitterMs":3,"samples":1800}]}]}
```
Windows: `1h`,`24h`,`7d`,`30d`,`60d` (clamped to available retention; `seconds` is the real covered span). `uptimePct` = 100 × (1 − seconds in a `down` incident / covered seconds).

### GET /v1/incidents?since=<unix, default now-7d>&limit=<default 200> — auth
`[{"id":"i-1","targetId":"..","targetName":"..","kind":"down","startedAt":..,"endedAt":null,"detail":"3 consecutive failures: timeout","peakMs":null}]` newest first; open incidents have `endedAt: null`.
Incidents persist across restarts. If `rules.webhookUrl` is set the agent POSTs JSON `{"event":"opened"|"closed","node":"<nodeName>","incident":{...}}` (5 s timeout, no retry).

## Diagnostics

### GET /v1/system — auth
```json
{"hostname":"..","os":"linux","kernel":"6.6.31","arch":"arm64","model":"Raspberry Pi 4 Model B","uptimeSec":1,"loadAvg":[0.1,0.1,0.1],
 "memTotalMB":3906,"memUsedMB":412,"diskFreeMB":20000,"tempC":48.2,
 "interfaces":[{"name":"eth0","kind":"ethernet","addrs":["192.168.50.20"],"up":true,"rxBytes":1,"txBytes":1}],
 "gateway":"192.168.50.1","defaultInterface":"eth0",
 "wifi":{"iface":"wlan0","ssid":null,"rssiDbm":-61,"linkQuality":70},
 "tailscale":{"selfIp":"100.100.0.1","peers":[{"name":"pi-b","ip":"100.100.0.2","online":true,"path":"direct","relay":null,"curAddr":"1.2.3.4:41641"}]}}
```
`kind`: ethernet|wifi|tailscale|loopback|other. `wifi` and `tailscale` null when unavailable (tailscale via `tailscale status --json`, cached 10 s).
Everything optional must degrade gracefully (agent must also run on macOS for development).

### POST /v1/traceroute — auth. Body `{"host":"1.1.1.1","maxHops":20}` → 
`{"host":"1.1.1.1","ip":"1.1.1.1","hops":[{"ttl":1,"ip":"192.168.50.1","name":null,"rttMs":[1.2,1.1,null],"reached":false}]}` (3 probes/hop, 1 s timeout each). Runs up to 30 s. Max 2 concurrent traceroutes (else 429).

### POST /v1/speedtest — auth → `{"at":..,"url":"..","bytes":25000000,"durationMs":1800,"downMbps":111.1,"latencyMs":24.0,"error":null}`; GET /v1/speedtest/history — auth → last 50, newest first.

### GET /metrics — auth (unless `metricsPublic`) Prometheus text: `imonitor_rtt_ms{target,name,kind}`, `imonitor_loss_pct`, `imonitor_up` (1/0), `imonitor_incidents_open`.

## Lifecycle
* `POST /v1/restart` — auth; replies 202 then restarts the agent. Linux/macOS: exits 0 and the service manager restarts it
  (systemd `Restart=always`, launchd `KeepAlive`). Windows service / scheduled task: re-initialises in-process (`--self-restart`).
* `POST /v1/reset-history` — auth; wipes samples+rollups+incidents (keeps config/clients).
* CLI (all OSes): `imonitord [run] [--listen :7878] [--state-dir DIR] [--log-file FILE]`, `imonitord pair-code [--json] [--listen]`
  (calls `/v1/local/pair-code` on 127.0.0.1; port taken from `--listen`, `$IMONITOR_LISTEN`, the installed service, else 7878),
  `imonitord version`, `imonitord install [--listen :7878]`, `imonitord uninstall [--purge]`.
* Default state dir when `--state-dir` is omitted: Linux/macOS `/var/lib/imonitor`, Windows `%ProgramData%\InternetMonitor`;
  when that is not writable (not root / not elevated) the agent falls back to a per-user dir (`~/.local/state/imonitor`,
  `~/Library/Application Support/imonitor`, `%LOCALAPPDATA%\InternetMonitor`).
* `install` / `uninstall` per OS (idempotent upgrade that preserves state; `--purge` also deletes the state dir):
  * **Linux** (root): copies itself to `/usr/local/bin/imonitord`, writes + enables `imonitord.service` — hardened, `DynamicUser=yes`,
    `StateDirectory=imonitor`, `AmbientCapabilities=CAP_NET_RAW`, `Restart=always`. Without systemd it installs the binary and prints
    the manual run command.
  * **macOS** (sudo): `/usr/local/bin/imonitord`, LaunchDaemon `/Library/LaunchDaemons/com.wasim.imonitord.plist` (`RunAtLoad`,
    `KeepAlive`, logs `/var/log/imonitord.log`), state `/var/lib/imonitor` (0700), loaded with `launchctl bootstrap system`.
  * **Windows** (elevated): `%ProgramFiles%\InternetMonitor\imonitord.exe`, Windows service `imonitord` (auto start, binPath
    `... run --service ...`, SCM handshake done natively, restart-on-failure actions), state `%ProgramData%\InternetMonitor`
    (ACL: SYSTEM + Administrators), log `imonitord.log` in the state dir, firewall rule "InternetMonitor agent (imonitord)" for
    inbound TCP on the listen port. Falls back to a SYSTEM scheduled task `InternetMonitor\imonitord` (at boot) if the service
    cannot start; `install --task` forces that.
* Service name `imonitord`. Memory budget: < 25 MB RSS with 20 targets @ 2 s.
* Platforms: linux/{amd64,386,arm64,arm(v7)}, darwin/{amd64,arm64}, windows/{amd64,arm64}. ICMP: raw socket or unprivileged
  datagram socket on Linux/macOS, `IcmpSendEcho` (iphlpapi) on Windows; traceroute falls back to `traceroute(8)` / `tracert -d`.
  `/v1/system` fields that an OS cannot provide (e.g. Windows `loadAvg`, `tempC`, `wifi`, interface byte counters) are null/empty.

## Storage (state dir)
`config.json` (0600; contains client tokens), `identity.json`, `incidents.jsonl`, `raw-YYYYMMDDHH.jsonl` (hourly segments, deleted past retention),
`rollup-YYYYMMDD.jsonl` (1-minute rollups: t, target, n, fails, min, avg, max, p95). Writes buffered and flushed every 5 s; torn last lines tolerated on load.
