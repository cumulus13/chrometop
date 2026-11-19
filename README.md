# ⚡ ChromeTop

**ChromeTop** is an `htop`/`ctop`-style terminal monitor and tab controller for Google Chrome built with **Go** (`Bubble Tea` + `Lip Gloss`) and a **Manifest V3 Chrome Extension** connected over **WebSocket**.

It runs directly with standard Chrome profiles without requiring `--remote-debugging-port`.

---

## ✨ Features

- **Real-Time Tab & Heap Telemetry:** Tracks open tabs, windows, active/pinned/audible/muted/discarded states, DOM node counts, JS heap usage (`performance.memory` via content script), and estimated memory footprint.
- **Interactive Tab Controls:** Focus (`Enter`/`f`), inspect details (`i`), reload (`r`), discard (`d`), mute/unmute (`m`), pin/unpin (`p`), duplicate (`c`), open a new tab (`n`), copy URL (`y`), or close (`x`).
- **Sorting & Live Search:** Press `/` to filter by title or URL; press `s` to cycle sorting (`EST-MB`, `JS-HEAP`, `TAB-ID`, `TITLE`).
- **Color + Emoji TUI:** Dynamic footprint gauge, status icons, selected row highlighting, and responsive terminal resizing.
- **Resilient WebSocket Bridge:** Automatic reconnect loop, WebSocket ping/pong heartbeats, MV3 `chrome.alarms` keepalive, and optional shared token authentication.

---

## 🚀 Build & Run

### Using `Makefile` (Linux / macOS / Git Bash)

```bash
make build
make run
```

With an optional auth token:

```bash
make run TOKEN="my-secret-token"
```

### Using `build.ps1` (Windows PowerShell)

```powershell
.\build.ps1 -Run
```

### Manual Go CLI

```bash
go mod tidy
go build -o chrometop ./cmd/chrometop
./chrometop --addr 127.0.0.1:9333
```

---

## 🧩 Install the Chrome Extension

1. Open `chrome://extensions` in Google Chrome.
2. Enable **Developer mode** (top right).
3. Click **Load unpacked** and select the `extension/` folder.
4. Click the extension toolbar icon if you need to customize the WebSocket URL or auth token.

---

## ⌨️ Keybindings

| Key | Action |
| :--- | :--- |
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `g` / `G` | Jump to top / bottom |
| `Enter` / `f` | Focus window & activate tab |
| `i` | Open tab detail inspector |
| `r` | Reload tab |
| `d` | Discard tab to free memory |
| `m` | Mute / unmute tab |
| `p` | Pin / unpin tab |
| `c` | Duplicate tab |
| `n` | Open new tab |
| `y` | Copy tab URL to clipboard |
| `x` / `Del` | Close tab |
| `s` | Cycle sort order |
| `/` | Filter by title or URL (`Esc` clears) |
| `R` | Force immediate telemetry sync |
| `q` / `Ctrl+C` | Quit ChromeTop |
