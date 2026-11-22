# ⚡ ChromeTop

[![CI](https://github.com/cumulus13/chrometop/actions/workflows/ci.yml/badge.svg)](https://github.com/cumulus13/chrometop/actions/workflows/ci.yml)
[![Release](https://github.com/cumulus13/chrometop/actions/workflows/release.yml/badge.svg)](https://github.com/cumulus13/chrometop/actions/workflows/release.yml)

**ChromeTop** is an `htop`-style terminal monitor and tab controller for **Chromium, Google Chrome, Brave, Edge and Vivaldi**, written in Go with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss).

It shows per-tab CPU, network, JS heap and memory estimates in a live, sortable table, and lets you focus, reload, close, mute, pin, duplicate or discard tabs from the keyboard.

---

## ✨ Features

- **Two connection modes** – a Manifest V3 extension (no browser restart needed) or direct Chrome DevTools Protocol (CDP).
- **Task-manager style table** – sort by CPU, network, memory, JS heap, PID, tab ID or title, ascending or descending.
- **Tab controls** – focus, inspect, reload, discard, mute, pin, duplicate, open, copy URL, close.
- **Live filter** – narrow the list by title or URL while you type.
- **Fills the terminal exactly** – the layout is computed from the real terminal size, so the footer always sits on the last row, and it re-flows on resize.
- **Stays responsive** – no UI blocking on network I/O, bounded polling time and coalesced updates (see [Architecture](ARCHITECTURE.md)).
- **Selection follows the tab** – when rows re-sort every second, the highlighted tab stays highlighted.
- **Safe rendering** – page titles and URLs are sanitized, so a malicious page cannot inject terminal escape sequences.

## 📦 Install

Download a prebuilt archive from the [Releases](https://github.com/cumulus13/chrometop/releases) page (Linux, macOS and Windows, amd64 and arm64), or build from source (Go 1.24+):

```bash
git clone https://github.com/cumulus13/chrometop.git
cd chrometop
make build          # Linux / macOS  -> ./chrometop
.\build.ps1         # Windows        -> chrometop.exe
```

Check the installed version with `chrometop --version`.

## 🚀 Quick start

### Mode A – Extension (default, no debug flags)

1. Start ChromeTop: `./chrometop`
2. Open `chrome://extensions` (or `brave://extensions`) and enable **Developer mode**.
3. Click **Load unpacked** and select the `extension/` folder.
4. The extension badge turns green (`LIVE`) and tabs appear in the table.

### Mode B – Direct CDP

Start the browser with remote debugging enabled, then attach:

```bash
chromium --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 --user-data-dir=/tmp/chromium-debug
./chrometop --cdp http://127.0.0.1:9222
```

> Chromium and Chrome 136+ require a non-default `--user-data-dir` when remote debugging is enabled.

## ⚙️ Command-line flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--addr` | `127.0.0.1:9333` | WebSocket listen address for extension mode |
| `--token` | *(empty)* | Optional shared token the extension must present |
| `--cdp` | *(empty)* | Remote debugging URL; enables direct CDP mode |
| `--interval` | `1s` | Polling interval in CDP mode |
| `--new-tab-url` | `about:blank` | URL opened by the `n` key |
| `--version` | | Print version and exit |

## ⌨️ Keybindings

| Key | Action |
| :--- | :--- |
| `↑` / `k`, `↓` / `j` | Move selection |
| `PgUp` / `PgDn` | Move one page |
| `g` / `Home`, `G` / `End` | Jump to top / bottom |
| `Enter` / `f` | Focus window and activate tab |
| `i` | Open the tab inspector |
| `r` | Reload tab |
| `d` / `m` / `p` | Discard / mute / pin tab *(extension mode)* |
| `c` | Duplicate tab |
| `n` | Open new tab |
| `y` | Copy tab URL to clipboard |
| `x` / `Del` | Close tab |
| `s` | Cycle sort column |
| `o` / `S` | Toggle ascending / descending |
| `/` | Filter by title or URL (`Esc` clears) |
| `R` | Force a telemetry sync |
| `q` / `Ctrl+C` | Quit |

## 🔎 Metrics: what is real, what is estimated

| Metric | Extension mode | CDP mode |
| :--- | :--- | :--- |
| CPU % | `chrome.processes` when available, else long-task estimate | `Performance.getMetrics` `TaskDuration` delta (main thread) |
| Network | `webRequest` byte counts + resource timing | Sum of `PerformanceResourceTiming.transferSize` |
| JS heap | `performance.memory` | `JSHeapUsedSize` / `JSHeapTotalSize` |
| Memory (MEM) | Process memory when available, else heuristic | **Heuristic** (35 MB + JS heap) |

Values marked as heuristic are estimates, not OS-level measurements.

## 📚 Documentation

| Document | Contents |
| :--- | :--- |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, data flow, concurrency model, layout engine (Mermaid diagrams) |
| [docs/USAGE.md](docs/USAGE.md) | Modes, flags and workflows in detail |
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | Extension ⇄ ChromeTop WebSocket protocol |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Building, testing, CI and releasing |
| [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) | Common problems and performance tips |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to contribute |
| [SECURITY.md](SECURITY.md) | Security model and reporting |
| [CHANGELOG.md](CHANGELOG.md) | Release history |

## 🗂️ Project layout

```text
cmd/chrometop/     entry point and flags
internal/hub/      thread-safe state store, WebSocket server, change notifications
internal/cdp/      direct CDP backend (poller, sessions, dispatcher)
internal/ui/       Bubble Tea model, layout and rendering
internal/protocol/ shared message and state types
extension/         Manifest V3 browser extension
.github/           CI, release and Dependabot configuration
```

## 👤 Author

[Hadi Cahyadi](mailto:cumulus13@gmail.com)

[![Buy Me a Coffee](https://www.buymeacoffee.com/assets/img/custom_images/orange_img.png)](https://www.buymeacoffee.com/cumulus13)

[![Donate via Ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/cumulus13)

[Support me on Patreon](https://www.patreon.com/cumulus13)
