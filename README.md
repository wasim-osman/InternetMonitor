<p align="center"><img src="assets/icon-1024.png" width="120" alt="Internet Monitor"></p>

# Internet Monitor

Monitor internet connectivity and latency **between your own machines** — and out to any address you choose — over time.
Tiny agents run on your nodes (Raspberry Pis, servers, PCs), do the probing and keep the history; a desktop app discovers
them, pairs with them, controls them and graphs everything.

* **Distributed probing** – each node measures from its own point of view (ICMP ping, TCP connect, HTTP timing, DNS).
* **Two months of history** stored on the nodes (1-minute rollups), so graphs survive your laptop being off.
* **Discovery wizard** – scans every network the computer is on (LAN, Wi-Fi, Tailscale…), excludes the machine you're on,
  recognises agents that are already running and pairs with them.
* **One-click agent install/removal over SSH** for Linux nodes (removal deletes the binary, service, config, tokens and history).
* **Stacked graphs** per node and target with loss shading, hover, drag-to-zoom, ranges from 5 minutes to 2 months.
* Packet loss, jitter, p95, uptime/SLA, incidents timeline, alerts (notifications, webhook), traceroute, speed tests,
  Tailscale direct/relay detection, CSV/PNG/PDF export, menu-bar / tray status, automatic updates.

## Downloads

Get them from the [latest release](https://github.com/wasim-osman/InternetMonitor/releases/latest).

| What | Files |
|---|---|
| **Desktop app – macOS** (Apple silicon + Intel, macOS 14+) | `InternetMonitor-macOS-universal.zip` |
| **Desktop app – Windows 10/11 x64** | `InternetMonitor_<ver>_x64-setup.exe` |
| **Desktop app – Linux x64** | `InternetMonitor_<ver>_amd64.AppImage`, `.deb` |
| **Agent – Linux** | `imonitord-linux-amd64`, `-386`, `-arm64`, `-arm` (Raspberry Pi) |
| **Agent – macOS** | `imonitord-darwin-arm64`, `imonitord-darwin-amd64` |
| **Agent – Windows** | `imonitord-windows-amd64.exe`, `imonitord-windows-arm64.exe` |

`SHA256SUMS` covers every file.

> The apps are not code-signed/notarized. macOS: right-click → Open the first time (or `xattr -dr com.apple.quarantine InternetMonitor.app`).
> Windows: SmartScreen may warn → *More info → Run anyway*.

## Installing an agent

Easiest: let the desktop app do it (it uses your SSH keys; the node needs passwordless `sudo`). Or by hand:

```sh
# Linux / macOS (downloads the right binary, verifies its checksum, installs the service)
curl -fsSL https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.sh | sh
```
```powershell
# Windows, elevated PowerShell
iwr -useb https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.ps1 | iex
```
Then `imonitord pair-code` on the node prints a 6-digit code to enter in the app. See [`agent/README.md`](agent/README.md).
Agents listen on TCP **7878**; only expose that port to networks you trust (pairing tokens are required for everything except identity).

## Building from source

```sh
cd agent && make test release         # all agents → agent/dist (Go, standard library only)
./build-app.sh                        # macOS app (SwiftUI, universal) → dist/InternetMonitor.app
cd desktop && npm ci && npx tauri build   # Windows/Linux app (Tauri 2)
```
Repository layout: `agent/` (Go), `app/` (SwiftUI macOS app), `desktop/` (Tauri app), `docs/API.md` (agent HTTP API),
`scripts/` (agent install scripts), `assets/` (icon).

## Releases & auto-update

Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml`, which builds every artifact and publishes the release.
With **Settings → Automatic updates** on, the apps check this repository's latest release, verify the download against
`SHA256SUMS`, and install it. Agents can be upgraded from the app's node *Control* page.

## License

[MIT](LICENSE) © 2026 Wasim Osman — Developed by Wasim Osman · <https://github.com/wasim-osman/InternetMonitor>
