# FlowDrop

[简体中文](README.md) · English

A self-hosted **shared inbox across your devices**: send text, links, images, APKs and ordinary files between your computer and Android phone. Send once — every device on the same Hub sees it immediately.

- **Self-hosted single-binary Hub**: one Go process runs REST + WebSocket + SQLite + disk attachments, with the frontend embedded in the same binary.
- **Clients everywhere**: native Android app, Linux / Windows desktop app, browser web UI, and a CLI.
- **Networking**: plain HTTP on a trusted home LAN; your existing Tailscale when you leave it; one Hub can bind a LAN address and a Tailscale address at once.
- **No accounts**: the Hub generates a four-digit pairing code, and a device token is remembered server-side and can be revoked.

## Documentation

Architecture [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) · Deployment and config [docs/DEPLOY.md](docs/DEPLOY.md) · Pairing [docs/PAIRING.md](docs/PAIRING.md) · CLI and HTTP usage [docs/CLI_AGENT.md](docs/CLI_AGENT.md) — these docs are currently written in Chinese.

## Features

- Send text and links (http/https only); links are clickable, text is rendered as plain text.
- Image upload with inline preview (JPEG/PNG/GIF/WebP) and click-to-zoom.
- Arbitrary file upload (including APKs) with progress, authenticated download, and SHA-256 display/copy.
- Drag and drop, pasted screenshots, and multi-file selection; the desktop app accepts a multi-file drop anywhere in the content window.
- Stable reverse-chronological paging (keyset cursor) and loading older inbox items.
- Manual deletion cleans up attachments; temporary files, orphaned attachments and expired sessions are swept periodically.
- WebSocket notifications, with automatic reconnect and a refresh whenever the window returns to the foreground.
- Pairing (four-digit code) and device revocation, plus rate limits, Host / Origin / CSRF checks and uniform security headers.
- LAN discovery (mDNS/DNS-SD, `_flowdrop._tcp`) and optional `auto_bind` hot re-binding.
- CLI delivery via `flowdrop login` / `flowdrop send` (`--json` NDJSON); on Android, multi-computer switching, automatic route selection, a pending queue and QR pairing.

## Quick start

**Desktop app (three steps)**

1. Start the desktop app: double-click `flowdrop-desktop.exe` (Windows) or run the installed `flowdrop-desktop` (Linux).
2. Click "Add device" in the settings window to get a four-digit pairing code (a QR code appears too when a phone-reachable address exists).
3. Open FlowDrop on the phone, pick or type the Hub address (for example `http://192.168.1.10:8080`), and enter the pairing code.

CLI, minimal flow:

```bash
flowdrop serve                    # terminal 1: start the Hub, http://127.0.0.1:8080 by default
flowdrop login                    # terminal 2: register this machine as a CLI device
flowdrop send --text - < note.txt # deliver after login
```

## Download and install

Prebuilt artifacts ship with the [latest Release](https://github.com/xyz0z0/flowdrop/releases/latest) (currently **v0.0.3**). Download only the files you need and compare each against its line in `SHA256SUMS.txt`, character by character.

| Platform | Asset |
|---|---|
| Windows desktop (no installer) | `flowdrop-windows-amd64.zip`, `flowdrop-windows-arm64.zip` |
| Linux desktop (amd64) | `flowdrop-desktop-linux-amd64.tar.gz` |
| Android (side-loading) | `flowdrop-android-0.0.3.apk` |
| CLI · Linux / macOS | `flowdrop-cli-{linux,darwin}-{amd64,arm64}.tar.gz` |
| CLI · Windows | `flowdrop-cli-windows-{amd64,arm64}.zip` |

```bash
sha256sum flowdrop-desktop-linux-amd64.tar.gz                  # Linux
grep ' flowdrop-desktop-linux-amd64.tar.gz$' SHA256SUMS.txt
shasum -a 256 flowdrop-cli-darwin-arm64.tar.gz                 # macOS (no sha256sum)
Get-FileHash .\flowdrop-windows-amd64.zip -Algorithm SHA256     # PowerShell
Select-String -Path .\SHA256SUMS.txt -Pattern 'flowdrop-windows-amd64.zip'
```

`SHA256SUMS.txt` covers every Release asset; `sha256sum -c` only applies once you have downloaded all of them. Every archive root carries `LICENSE`, `THIRD_PARTY_NOTICES.md` and `third_party/licenses/`.

### Windows desktop

Unzip anywhere and double-click `flowdrop-desktop.exe`; nothing is installed and no system directory is written.

- On first start, allow "Private networks" in the Windows Firewall prompt, otherwise phones on the same LAN cannot find this computer.
- The UI is rendered by WebView2 (bundled with Windows 11; older systems need the Microsoft Edge WebView2 Runtime). The build is not code-signed: on a SmartScreen prompt choose "More info" → "Run anyway".
- Data lives in `%LocalAppData%\flowdrop\`, settings in `%AppData%\flowdrop\`, logs in `desktop.log`; closing the window only hides it in the tray, and the tray menu exits for good.

### Linux desktop

The tarball bundles the binary, install script, `.desktop` template and icons, so nothing needs building before you install:

```bash
tar -xzf flowdrop-desktop-linux-amd64.tar.gz && cd flowdrop-desktop-linux-amd64
./install-desktop.sh              # installs into ~/.local
./install-desktop.sh --uninstall  # removes the launcher, keeps Hub data and settings
```

Runtime dependencies (Debian/Ubuntu package names): `libgtk-3-0t64 libwebkit2gtk-4.1-0 libsoup-3.0-0 libjavascriptcoregtk-4.1-0` (older releases use `libgtk-3-0`). The scripts write only to your home directory: no autostart entry, no system service.

### Android (APK side-loading)

Download `flowdrop-android-0.0.3.apk`, allow "Install unknown apps", and side-load it — no app store is involved. Then pick the Hub from the discovery list or type its address, and enter the four-digit pairing code shown on the computer.

### CLI

The CLI and the Hub are the same binary:

```bash
tar -xzf flowdrop-cli-linux-amd64.tar.gz   # Windows: unzip flowdrop-cli-windows-amd64.zip
./flowdrop login                           # register this machine as a CLI device
./flowdrop send --text - < note.txt        # deliver after login
```

The Linux archive also carries `deploy/flowdrop.service` (a systemd user unit that listens on `127.0.0.1:8080` only) and `deploy/http-lan.env.example` (a trusted-LAN plain-HTTP template), but it does not install the service; see [docs/DEPLOY.md §4](docs/DEPLOY.md#4-常驻运行systemd-用户服务) for running it permanently.

## Build from source

**Requirements**: Go 1.26+ (`desktop/` and `tools/winres/` declare `go 1.26.0`; the `server/` module itself needs only Go 1.24+), Node.js 20+, npm; desktop builds also need the GTK3 / WebKitGTK development packages.

```bash
git clone https://github.com/xyz0z0/flowdrop.git flowdrop && cd flowdrop
FLOWDROP_COOKIE_SECURE=false scripts/dev.sh   # terminal 1: Hub on :8080 + Vite on :5173
(cd server && go run ./cmd/flowdrop pair)     # terminal 2: four-digit code while the Hub runs
```

In development Vite serves <http://127.0.0.1:5173> and proxies `/api` and WebSocket traffic to Go, so you must use port 5173; `FLOWDROP_COOKIE_SECURE` defaults to `true`, and the flag above turns it off locally. Enter the generated code at 5173 to pair.

```bash
scripts/build.sh 0.1.0        # frontend build + embed → dist/flowdrop (single file)
./dist/flowdrop doctor        # environment self-check; serve defaults to http://127.0.0.1:8080
```

**Desktop (Linux)**

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
scripts/build-desktop.sh      # → dist/flowdrop-desktop
scripts/install-desktop.sh    # installs into ~/.local, no sudo
```

**Android**: JDK 17+ and Android SDK Platform 35 + Build-Tools 35; full details in [android/README.md](android/README.md).

```bash
cd android && ./gradlew :app:assembleDebug
# → android/app/build/outputs/apk/debug/app-debug.apk
```

## Hub networking and deployment

### Primary path: plain HTTP on the home LAN

When the Hub and your phone/computer share a Wi-Fi network, listen on the **specific LAN IP**; no TLS is needed:

```bash
# Example: the Hub LAN IP is 192.168.1.10 (check with ip -4 addr)
FLOWDROP_LISTEN=192.168.1.10:8080 FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
```

- `FLOWDROP_LISTEN` must be a concrete IP, never `0.0.0.0` / `::`: with `cookie_secure=false` the server **refuses** to start on a wildcard address; the listen IP is added to the Host allowlist automatically and `FLOWDROP_ALLOWED_HOSTS` only adds other names such as mDNS or Tailscale MagicDNS names.
- Plain HTTP is not a secure context, so browsers may disable automatic clipboard and similar APIs (copy failures fall back to selectable text); trusted **home LAN only**, never port-forward 8080 to the internet.
- `scripts/lan-http.sh` probes and validates the local LAN IP before starting the Hub; `flowdrop doctor` checks ports and the pairing table beforehand.

### LAN discovery (mDNS/DNS-SD)

When the conditions hold, the Hub **publishes DNS-SD records by default** (`discovery_enabled` / `FLOWDROP_DISCOVERY_ENABLED`), one per eligible LAN listener:

| Field | Value |
|---|---|
| Service type | `_flowdrop._tcp.local.` |
| Instance name | `FlowDrop-<host nickname>` (safely truncated; conflicts get a `-2` suffix) |
| SRV | the Hub's real listening port |
| A | the RFC1918 LAN IPv4 that listener was bound to (never a Tailscale address) |
| TXT | `v=1`, `scheme=http`, `path=/` |

Publishing requires all of: a concrete RFC1918 IPv4 listen address (10/8, 172.16/12, 192.168/16), `cookie_secure=false`, and an address on a multicast-enabled NIC. Loopback, `0.0.0.0`/`::`, Tailscale `100.64/10`, link-local, public addresses, container/VM bridges derived by `auto_bind` (`docker0`, `br-*`, virbr and the like) and HTTPS reverse-proxy entries are never advertised (a bridge listed explicitly in `additional_listen` still follows explicit config); records carry no device tokens, sessions or machine paths.

```bash
FLOWDROP_DISCOVERY_ENABLED=false ./dist/flowdrop serve   # explicit off; the helper script uses scripts/lan-http.sh --no-discovery
```

mDNS needs no Avahi/Bonjour install; if another process holds UDP 5353 it only logs a warning and HTTP traffic is unaffected. See [docs/DEPLOY.md §5.7](docs/DEPLOY.md#57-局域网自动发现mdnsdns-sd) for troubleshooting.

### Binding LAN + Tailscale together

Listen on the LAN as usual and add Tailscale as an extra listener: one Hub, one port, one database. Phones use the LAN address at home and the Tailscale address elsewhere, with no restart or switching; `additional_listen` can also be an array in `~/.config/flowdrop/config.json`.

```bash
FLOWDROP_LISTEN=192.168.1.10:8080 FLOWDROP_ADDITIONAL_LISTEN=100.x.y.z:8080 \
FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve   # Tailscale only? use the output of tailscale ip -4 as LISTEN
```

Add the MagicDNS name to `FLOWDROP_ALLOWED_HOSTS` if you browse by name. Direct Tailscale access needs **no** `tailscale serve`; if you do expose HTTPS through it, keep `cookie_secure=true`. Optional hardening (`tailscale serve`, Caddy LAN HTTPS) and details are in [docs/DEPLOY.md §5.8](docs/DEPLOY.md#58-同时使用内网和-tailscale同一-hub-双监听).

### auto_bind (follow NIC changes)

With `auto_bind=true` the Hub binds every current RFC1918 LAN and Tailscale address on the **same port** as the main listener and rescans NICs every 5 seconds: new addresses come up and vanished ones go down without editing config or restarting.

```bash
FLOWDROP_AUTO_BIND=true FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
./dist/flowdrop doctor    # show which addresses would be bound, before starting
```

Loopback, link-local and public addresses are never auto-bound; RFC1918 addresses on container/VM bridges (`docker0`, `br-*`, virbr and the like) and on `veth`/`wg*`/`tun*` **are bound but never advertised over mDNS**. Derived binds are best-effort, unlike the all-or-nothing explicit `additional_listen`. Details in [docs/DEPLOY.md §5.9](docs/DEPLOY.md#59-自动绑定本机所有可用地址auto_bind).

## Clients

**Desktop (Linux + Windows, Wails v3.1.0)**

- Launching starts the same Go Hub in-process and opens the React inbox; closing the window hides it in the tray while the Hub keeps running (with no system tray it asks to quit instead, so no orphan process is left), and the tray menu can show the window, open settings, or explicitly quit and stop the Hub (withdrawing mDNS and releasing the port).
- The settings window picks a real local IPv4 (LAN / Tailscale labelled automatically) and port and can restart the Hub; dual mode and `auto_bind` are supported.
- "Add device" produces a four-digit code (shown locally only, never sent over the network, never logged) plus a pairing QR code; devices can be revoked locally.
- After login, drop images or files anywhere in the inbox window to upload them (directories are skipped, not recursed); an in-app direct proxy keeps the Hub's HTTP and WebSocket reachable while a system proxy is enabled.

**Android**

- Pairing-code login, device token in the Android Keystore, text/image/file send and receive, a system share target, live sync and downloads.
- Multi-computer switching and automatic route selection: pair each computer once, tap the computer name at the top to switch, and the last one is restored on start; each Hub's stable ID, device token and candidate addresses are stored, preferring LAN then Tailscale, retrying only the connection and never replaying writes.
- Pending queue: inbox items remember their original computer and never switch targets; items whose delivery cannot be confirmed are marked "delivery unconfirmed" and are resent only after the user confirms a manual retry.
- QR pairing only prefills the pairing code and candidate addresses; the user still confirms before submitting, and no automatic login happens.

**CLI**

- `flowdrop login` registers this machine as a device (generating a pairing code from the local database on the same host) and stores credentials in `~/.config/flowdrop/cli.json` (0600); `flowdrop send [--json] FILE...` accepts `--text TEXT|-` (stdin ≤64 KiB), `--link URL` and `--name NAME -`, uses exit codes 0/1/2 with no automatic retry, and `FLOWDROP_HUB` + `FLOWDROP_TOKEN` work without writing anything to disk. See [docs/CLI_AGENT.md](docs/CLI_AGENT.md).

## Security model

| Area | Measure |
|---|---|
| Transport | The default path is plain HTTP on a trusted home LAN; use Tailscale Serve or a TLS reverse proxy off-LAN or on untrusted networks |
| Auth | Four-digit temporary pairing code (120 s, single use; five cumulative failures lock pairing for the whole Hub until a new code is generated locally) + 32B device token (only its SHA-256 is stored), revocable |
| CSRF | Double-submit token plus Origin / Host checks; a Host allowlist blocks DNS rebinding; identical for web and native apps |
| Authorization | Every inbox item, preview, download and WebSocket is session-authenticated; knowing an ID is not enough |
| Uploads and files | Streaming writes, size cap, free-space precheck, concurrency cap, atomic rename; MIME sniffing, SVG/HTML/JS forced to download; file names never touch paths |
| Privacy | Logs contain no content, device tokens or sessions; no third-party analytics and no external resources |

## Known limitations

- **Plain HTTP is the primary path**: LAN traffic is unencrypted and same-network devices or a man in the middle can read content and cookies.
- **Browser APIs are limited over LAN HTTP**: `navigator.clipboard`, Service Workers and notifications may be unavailable.
- **The web UI does not switch addresses automatically**: cookies are per-origin, so LAN → Tailscale needs a new pairing; Android selects routes automatically.
- Browser cookie TTL is 7 days (`session_ttl_hours`) and then requires pairing again; a device token stays valid until revoked locally.
- Each Hub serves a single user with an independent inbox: no multi-tenancy, no end-to-end encryption, no automatic expiry (only an `expires_at` field).
- Uploads are capped at 512 MiB (configurable), with no resume: an interrupted transfer must be repeated.
- Over plain HTTP (`cookie_secure=false`) only concrete IPs are allowed: the server refuses `0.0.0.0` / `::`, while with `cookie_secure=true` a wildcard is allowed but only warned about. Changing an explicit `additional_listen` address needs a config edit and restart. mDNS is published only for a concrete RFC1918 LAN IPv4 over plain HTTP, and may fail when UDP 5353 is taken.
- Real Android devices, real Wi-Fi / Tailscale route selection and real LAN mDNS discovery are still **pending acceptance**; the desktop app covers Linux amd64 and Windows (arm64 cross-compile also **pending acceptance**), and there is no macOS desktop app.

## Documentation index

| Document | Contents |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Architecture and data model, auth sessions, upload pipeline, desktop and multi-listener design |
| [docs/DEPLOY.md](docs/DEPLOY.md) | Dependencies, build, running as a service, LAN / Tailscale / HTTPS, backup and restore, config reference |
| [docs/PAIRING.md](docs/PAIRING.md) · [docs/CLI_AGENT.md](docs/CLI_AGENT.md) | Pairing API and QR codes; CLI / HTTP usage for agents, with exit-code semantics |
| [android/README.md](android/README.md) | Android scope, protocol baseline, build and tests |
| [CONTRIBUTING.md](CONTRIBUTING.md) · [SECURITY.md](SECURITY.md) | Contribution workflow; how to report vulnerabilities |

## Repository layout

```text
flowdrop/
├── docs/          architecture, deployment, pairing, CLI usage
├── server/        Go Hub (cmd + internal)
├── web/           React + TS + Vite frontend
├── desktop/       Wails desktop shell (Linux / Windows)
├── android/       native Android app (build and test notes included)
├── tests/         Playwright end-to-end tests
├── deploy/        systemd user unit, Caddy LAN HTTPS template (optional)
└── scripts/       dev.sh / build.sh / lan-http.sh (plain-LAN helpers)
```

## License

Released under the [MIT License](LICENSE). Third-party licenses are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); please do not file security issues publicly — follow [SECURITY.md](SECURITY.md) instead.

Copyright (c) 2026 xyz0z0
