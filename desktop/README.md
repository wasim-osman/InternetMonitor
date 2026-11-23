# Internet Monitor — desktop (Windows / Linux)

Cross-platform dashboard and controller for `imonitord` agents, built with **Tauri 2** (Rust backend,
plain TypeScript + Vite frontend, no UI framework). Functional and visual parity target: the native macOS
app in `../app`. Agent API contract: `../docs/API.md`.

* App id `com.wasim.internetmonitor`, product name **Internet Monitor**, version 1.0.0, MIT.
* Developed by Wasim Osman — https://github.com/wasim-osman/InternetMonitor

## Layout

```
desktop/
  index.html, src/                 frontend (TypeScript, canvas charts, no framework)
    main.ts                        shell: sidebar, routing, banners
    state.ts                       model: saved nodes, polling (live 2 s / slow 5 s), notifications, tray status
    chart.ts                       hand-rolled canvas latency chart (band, avg, loss, crosshair, drag-zoom)
    wizardLogic.ts                 pure Targets-grid logic (unit tested)
    views/                         overview, node (latency/incidents/diagnostics/control), incidents, settings, wizard
    updater.ts                     update scheduling + banner
    selftest.ts                    in-webview end-to-end self-test (IM_SELFTEST=1 only)
  scripts/copy-agents.mjs          copies ../agent/dist/imonitord-linux-* → src-tauri/resources/agents
  src-tauri/
    src/agent.rs                   HTTP client (reqwest + rustls) — all agent traffic goes through Rust
    src/discovery.rs               interfaces, /24../22 sweeps, Tailscale peers, identity probes, rDNS, nodeId dedupe
    src/icmp.rs                    ICMP: SOCK_DGRAM/IPPROTO_ICMP (Linux/macOS), IcmpSendEcho (Windows), TCP fallback
    src/ssh.rs                     system ssh/scp (BatchMode): pair-code, install, uninstall (script verbatim from SSHInstaller.swift)
    src/store.rs                   nodes.json (0600 on unix) + settings.json in the OS app-data dir
    src/updater.rs                 GitHub-releases updater with SHA-256 verification
    src/tray.rs                    status-coloured tray icon + menu
    tests/                         live-agent integration tests (ignored by default) + updater HTTP test
```

Data dir: `%APPDATA%\com.wasim.internetmonitor` (Windows), `~/.local/share/com.wasim.internetmonitor` (Linux),
`~/Library/Application Support/com.wasim.internetmonitor` (macOS dev). `IM_DATA_DIR` overrides it.

## Build

```sh
npm ci
npm run agents                     # bundle Linux agents from ../agent/dist (or IM_AGENTS_DIR=...)
npx tauri dev                      # or: cargo tauri dev
npx tauri build --bundles nsis     # Windows
npx tauri build --bundles appimage,deb   # Linux (needs libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev patchelf)
npx tauri build --debug --bundles app    # macOS dev bundle
```

Cross-check the Windows target from macOS (ring needs clang-cl/llvm-lib, e.g. Homebrew LLVM):

```sh
cd src-tauri && PATH="$(brew --prefix llvm)/bin:$PATH" cargo xwin check --target x86_64-pc-windows-msvc
```

## Tests

```sh
npm run typecheck && npm test                       # tsc + vitest (formatting, CSV, palette parity, wizard grid)
cd src-tauri && cargo test && cargo clippy --all-targets
# against a real agent (e.g. agent/dist/imonitord-darwin-arm64 --listen 127.0.0.1:7899 --state-dir /tmp/st):
IM_TEST_AGENT=127.0.0.1:7899 cargo test --test live_agent -- --ignored --nocapture
```

End-to-end UI self-test (drives the real webview: wizard scan → manual add → pair → targets → apply →
overview / node tabs / export / settings, then prints `SELFTEST {json}` and exits):

```sh
IM_DATA_DIR=/tmp/im-data IM_SELFTEST=1 IM_SELFTEST_EXIT=1 IM_SELFTEST_AGENT=127.0.0.1:7899 \
  "src-tauri/target/debug/bundle/macos/Internet Monitor.app/Contents/MacOS/internet-monitor"
```

(Self-test mode allows adding a loopback agent, which the wizard otherwise rejects as "this machine".)

## Release assets (what CI uploads)

`.github/workflows/desktop.yml` (reusable: `workflow_call` + `workflow_dispatch`) builds and renames to these
**exact** names — the in-app updater looks for them:

| Platform | Asset name | Updater action |
|---|---|---|
| Windows x64 | `InternetMonitor_<ver>_x64-setup.exe` (NSIS, per-user install) | download → verify → launch installer → app exits |
| Linux x64 | `InternetMonitor_<ver>_amd64.AppImage` | download → verify → replace `$APPIMAGE` in place → "Restart to update" |
| Linux x64 | `InternetMonitor_<ver>_amd64.deb` | not auto-installed: the updater opens the release page |
| all | `SHA256SUMS` (`sha256sum` format, covering the files above) | **required** — without it, or on mismatch, nothing is installed |

`<ver>` has no leading `v` (e.g. `InternetMonitor_1.0.1_x64-setup.exe`); the release tag may be `v1.0.1`.
The workflow artifacts are `desktop-windows` and `desktop-linux`, each containing its files plus a
`SHA256SUMS.windows` / `SHA256SUMS.linux` fragment; the release job should publish
`cat SHA256SUMS.* > SHA256SUMS` alongside the assets. Agent binaries are taken from an artifact named `agents`
(produced by a sibling job) and copied to `src-tauri/resources/agents/` before building.

## Updater

On start (after ~8 s) and every 6 hours while **Settings → Automatic updates** is on (default), the app
calls `GET https://api.github.com/repos/wasim-osman/InternetMonitor/releases/latest`, compares the tag's semver
with its own version and, if newer, shows a banner. With automatic updates on it downloads the platform asset,
verifies it against `SHA256SUMS` and installs it as in the table above; with it off the banner offers
**Install update**. Drafts / pre-releases are ignored. There is no code signing: SHA-256 from the same
release protects against corrupted downloads, not against a compromised GitHub account. `IM_UPDATE_URL`
overrides the API URL (used by tests).

## Platform notes

* **ICMP discovery on Linux** uses unprivileged ping sockets; the user's group must be inside
  `net.ipv4.ping_group_range` (default on most desktop distros). Otherwise discovery falls back to TCP-connect
  probing (ports 7878, 22, 80, 443, 445, 139, 3389, 8080, 53 — a refused connection also counts as alive).
* **SSH** uses the system OpenSSH client (`ssh`/`scp` on PATH; Windows: `C:\Windows\System32\OpenSSH`) with
  `BatchMode=yes` — keys/agent only, never passwords. Install needs passwordless `sudo` on the node (or root).
* Closing the window keeps the app running in the tray (when the tray is enabled). Launch at login starts it
  minimised to the tray (`--minimized`).
