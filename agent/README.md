# imonitord — InternetMonitor agent

A single static Go binary (stdlib only) that runs on each monitored node: a Raspberry Pi, a Linux
PC or server, a Mac or a Windows machine. It does all the probing (ICMP / TCP / HTTP / DNS), keeps
all history on the node, and serves the JSON API from [`../docs/API.md`](../docs/API.md) on port
7878. The macOS app pairs with it and acts as the dashboard and controller.

## Binaries

`make release` builds all of them into `dist/` (plus `SHA256SUMS`):

| OS / CPU | Binary | Check |
| --- | --- | --- |
| Linux x86-64 (PCs, servers, VMs) | `imonitord-linux-amd64` | `uname -m` = `x86_64` |
| Linux 32-bit x86 | `imonitord-linux-386` | `i686` / `i386` |
| Linux ARM 64-bit (Pi 3/4/5 on 64-bit OS, ARM servers) | `imonitord-linux-arm64` | `aarch64` |
| Linux ARM 32-bit (32-bit Raspberry Pi OS, Pi 2/3/4/Zero 2) | `imonitord-linux-arm` (ARMv7) | `armv7l` |
| macOS Apple silicon | `imonitord-darwin-arm64` | `uname -m` = `arm64` |
| macOS Intel | `imonitord-darwin-amd64` | `x86_64` |
| Windows 10/11, Server 2019+ (x64) | `imonitord-windows-amd64.exe` | |
| Windows on ARM | `imonitord-windows-arm64.exe` | |

ARMv6 (Pi 1, original Pi Zero) is not supported. Go 1.26 needs Linux kernel 3.2+, macOS 12+ and
Windows 10 / Server 2016+.

## Quick install from GitHub releases

Linux or macOS (detects OS/CPU, checks the SHA-256, runs `sudo ./imonitord install`):

```sh
curl -fsSL https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.sh | sh
```

Windows, from an elevated PowerShell (Run as administrator):

```powershell
irm https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.ps1 | iex
```

Both accept `IMONITOR_LISTEN=:8080` (another port) and `IMONITOR_RELEASE_URL` (another asset
location). Both print a pairing code at the end.

## Install on Linux (Raspberry Pi or any systemd box)

```sh
# from this folder on your Mac
scp dist/imonitord-linux-arm64 pi@raspberrypi.local:imonitord

# on the Pi
ssh pi@raspberrypi.local
chmod +x imonitord
sudo ./imonitord install          # copies to /usr/local/bin, writes + starts imonitord.service
imonitord pair-code               # prints a 6-digit code (valid 10 min, single use)
```

Enter the code in the Mac app to pair. The app can also fetch a code over SSH with
`ssh pi@host imonitord pair-code --json`.

`install` is idempotent. To upgrade, copy the new binary over and run `sudo ./imonitord install`
again. The service restarts and keeps its identity, history and paired clients.

Options:

* `sudo ./imonitord install --listen :8080` uses another port. It is saved in the unit file, and
  `imonitord pair-code` picks it up from there.
* `sudo imonitord uninstall` stops and removes the service and binary but keeps the state.
* `sudo imonitord uninstall --purge` also deletes the state directory (history, identity, clients).

Without systemd (Alpine/OpenRC, containers, …) `install` copies the binary to
`/usr/local/bin/imonitord` and prints the command to start it from your init system:
`imonitord run --listen :7878 --state-dir /var/lib/imonitor` (as root, or with `CAP_NET_RAW` for ping).

### What the service looks like

* Unit `/etc/systemd/system/imonitord.service`. It runs with `DynamicUser=yes`,
  `StateDirectory=imonitor` (`/var/lib/imonitor`) and `AmbientCapabilities=CAP_NET_RAW` (raw ICMP
  for ping and traceroute, with no root needed). `Restart=always` is set, the usual systemd
  sandboxing is applied (`ProtectSystem=strict`, `NoNewPrivileges`, …) and `MemoryMax=64M`.
* Logs: `journalctl -u imonitord -f`
* Status: `systemctl status imonitord`

### Optional extras on Linux

* **Tailscale**: if `tailscale` is installed, `/v1/system` lists peers. Live results for
  `100.64.0.0/10` targets also get a `pathHint` (`direct` or `relay`).
* **Wi-Fi SSID**: comes from `iwgetid` (package `wireless-tools`) when it is present. RSSI and
  link quality come from `/proc/net/wireless` and need no extra package.

## Install on macOS (Intel or Apple silicon)

```sh
chmod +x imonitord-darwin-arm64            # or imonitord-darwin-amd64 on an Intel Mac
xattr -d com.apple.quarantine imonitord-darwin-arm64 2>/dev/null   # only if downloaded with a browser
sudo ./imonitord-darwin-arm64 install
imonitord pair-code
```

* Binary `/usr/local/bin/imonitord`, LaunchDaemon `/Library/LaunchDaemons/com.wasim.imonitord.plist`
  (`RunAtLoad`, `KeepAlive`, runs as root so raw ICMP works), state `/var/lib/imonitor` (0700).
* Logs: `/var/log/imonitord.log` (not rotated by the agent; it only logs events).
* Status: `sudo launchctl print system/com.wasim.imonitord`
* `sudo imonitord uninstall [--purge]` boots it out and removes the plist, binary and log.
* The binaries are not notarized; macOS may ask to allow incoming connections for `imonitord`
  the first time if the application firewall is on.

## Install on Windows (10/11, Server 2019+)

From an elevated Command Prompt or PowerShell (Run as administrator):

```powershell
.\imonitord-windows-amd64.exe install       # or imonitord-windows-arm64.exe
& "$env:ProgramFiles\InternetMonitor\imonitord.exe" pair-code
```

* Copies itself to `%ProgramFiles%\InternetMonitor\imonitord.exe` and registers the Windows
  service **imonitord** ("InternetMonitor agent"): automatic start, restarts on failure, runs as
  LocalSystem. The binary speaks the Service Control Manager protocol itself (`run --service`).
* State: `%ProgramData%\InternetMonitor` (only SYSTEM and Administrators can read it), log
  `%ProgramData%\InternetMonitor\imonitord.log` (rotated at 10 MB on start).
* Adds the Windows Firewall rule "InternetMonitor agent (imonitord)" for inbound TCP 7878 (or the
  `--listen` port). `uninstall` removes it.
* If the service cannot be started, `install` falls back to a scheduled task
  `InternetMonitor\imonitord` that runs as SYSTEM at boot. `install --task` forces that mode.
* Status: `sc query imonitord` (or Services.msc). Stop/start: `sc stop imonitord` / `sc start imonitord`.
* `uninstall [--purge]` removes the service (and task), firewall rule and binary.
* Ping uses `IcmpSendEcho` (no admin or raw sockets needed); traceroute uses the same API with an
  increasing TTL and falls back to `tracert -d`. The gateway comes from `GetBestRoute`
  (fallback: `route print`). Load average, temperature, Wi-Fi and interface byte counters are
  reported as `null` on Windows.
* Tailscale is found on `PATH` or in `C:\Program Files\Tailscale\tailscale.exe`.

## Run manually / development (any OS)

```sh
make test release                  # vet + unit tests, then cross-compile every target into dist/
make vet-all                       # go vet for every release GOOS/GOARCH
./dist/imonitord-darwin-arm64 run --listen 127.0.0.1:7899 --state-dir /tmp/imon
./dist/imonitord-darwin-arm64 pair-code --listen :7899
```

Without `--state-dir` the agent uses the system state dir when it is writable (root /
Administrator) and otherwise a per-user one, so `./imonitord` just works unprivileged:
`~/.local/state/imonitor` (Linux), `~/Library/Application Support/imonitor` (macOS),
`%LOCALAPPDATA%\InternetMonitor` (Windows).

Unprivileged, ICMP uses datagram ICMP sockets (macOS; Linux when `net.ipv4.ping_group_range`
allows it) and traceroute falls back to `traceroute(8)`. macOS reports temperature, Wi-Fi and
interface counters as `null`.

CLI summary:

```
imonitord [run] [--listen :7878] [--state-dir DIR] [--log-file FILE]
imonitord pair-code [--json] [--listen :7878]
imonitord version
sudo imonitord install [--listen :7878]          # Windows: elevated, optional --task
sudo imonitord uninstall [--purge]
```

`run --service` is only used in the Windows service's command line; `run --self-restart` makes
`POST /v1/restart` restart in-process instead of exiting (used where no supervisor relaunches the
process).

## State directory

| File | Contents |
| --- | --- |
| `identity.json` | stable `nodeId` (UUID) |
| `config.json` (0600) | config document plus paired clients (only SHA-256 hashes of their tokens) |
| `raw-YYYYMMDDHH.jsonl` | raw samples, one hourly segment each (UTC), deleted after `retentionRawHours` |
| `rollup-YYYYMMDD.jsonl` | 1-minute rollups (UTC days), deleted after `retentionRollupDays` |
| `incidents.jsonl` | incidents, append-only (the last line per id wins), compacted on retention |
| `speedtest.json` | last 50 speed-test results |
| `imonitord.log` | Windows only: service log |

Writes are buffered and flushed every 5 s. Torn lines left by a crash are skipped on load.
