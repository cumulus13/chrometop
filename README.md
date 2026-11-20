# ⚡ ChromeTop

**ChromeTop** is an `htop`/`ctop`-style terminal monitor and tab/target controller for **Chromium, Google Chrome, Brave, Edge, and Vivaldi** built with **Go** (`Bubble Tea` + `Lip Gloss`).

ChromeTop supports **two connection modes**:

1. **Extension Mode (Default — No Debug Flags Required):** Uses the included Manifest V3 Chrome/Chromium extension (`extension/`) connected over WebSocket to `127.0.0.1:9333`. Works out-of-the-box on standard Chromium/Chrome/Brave profiles without restarting your browser.
2. **Direct CDP Mode (`--cdp` — Optional for Chromium/Brave/Chrome with `--remote-debugging-port`):** Connects directly to Chromium's Chrome DevTools Protocol WebSocket endpoints when you launch Chromium/Brave with `--remote-debugging-port=9222`, collecting per-target `TaskDuration` CPU %, network transfer rates, JS heap metrics, and DOM node counts without installing an extension.

---

## ✨ Features

- **CPU, Network, JS Heap & Memory Telemetry:**
  - **Extension Mode:** Uses `chrome.processes` (on Chromium Dev/Canary or enterprise-enabled builds) with automatic fallback to main-thread `longtask` CPU scoring, `webRequest` + `PerformanceResourceTiming` network byte rates, and `performance.memory` JS heap stats.
  - **Direct CDP Mode (`--cdp`):** Queries `Performance.getMetrics` (`TaskDuration`, `JSHeapUsedSize`, `JSHeapTotalSize`, `Nodes`) and resource `transferSize` directly over per-page CDP WebSockets.
- **Task Manager-Style Sorting (ASC / DESC):** Press `s` to cycle sort columns (`CPU%`, `NET/S`, `EST-MB`, `JS-HEAP`, `PID`, `TAB-ID`, `TITLE`) and `o` (or `S`) to toggle **Ascending (`▲`) / Descending (`▼`)**.
- **Interactive Tab Controls:** Focus (`Enter`/`f`), inspect (`i`), reload (`r`), discard (`d`), mute/unmute (`m`), pin/unpin (`p`), duplicate (`c`), open new tab (`n`), copy URL (`y`), or close (`x`).
- **Live Filter:** Press `/` to filter tabs by title or URL in real time.

---

## 🚀 Build & Run

### 1. Build the Go Binary

**Windows PowerShell:**
```powershell
.\build.ps1
```

**Linux / macOS:**
```bash
make build
```

---

### Mode A: Chrome / Chromium / Brave Extension Mode (Default)

1. Start ChromeTop:
   ```bash
   ./chrometop --addr 127.0.0.1:9333
   ```
2. Open `chrome://extensions` (or `brave://extensions`) in Chromium/Chrome/Brave.
3. Enable **Developer mode** (top right).
4. Click **Load unpacked** and select the `extension/` folder.
5. Verify the extension badge turns green (`LIVE`).

---

### Mode B: Direct CDP Mode (`--cdp` with `--remote-debugging-port`)

If you run **Chromium**, **Brave**, or **Chrome** with `--remote-debugging-port`, you can attach ChromeTop directly via `--cdp` without any browser extension:

1. Launch Chromium (or Brave/Chrome) with remote debugging enabled on localhost:
   - **Linux / macOS (Chromium):**
     ```bash
     chromium --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1
     ```
   - **Windows PowerShell (Chromium / Brave / Chrome):**
     ```powershell
     chrome.exe --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1
     # or for Brave:
     # brave.exe --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1
     ```
   > **Note on Chromium 136+ / Chrome 136+:** Newer Chromium releases require a non-default `--user-data-dir` when enabling `--remote-debugging-port` on your primary profile (for example: `--remote-debugging-port=9222 --user-data-dir=/tmp/chromium-debug`).

2. Start ChromeTop in CDP mode:
   ```bash
   ./chrometop --cdp http://127.0.0.1:9222
   ```

---

## ⌨️ Keybindings

| Key | Action |
| :--- | :--- |
| `↑` / `k` | Move selection cursor up |
| `↓` / `j` | Move selection cursor down |
| `g` / `G` | Jump to top / bottom |
| `Enter` / `f` | Focus window and activate tab |
| `i` | Open full tab telemetry inspector |
| `r` | Reload tab |
| `d` | Discard (suspend) tab *(Extension mode)* |
| `m` | Mute / unmute tab *(Extension mode)* |
| `p` | Pin / unpin tab *(Extension mode)* |
| `c` | Duplicate tab |
| `n` | Open new tab |
| `y` | Copy tab URL to clipboard |
| `x` / `Del` | Close tab |
| `s` | Cycle sort column (`CPU%` → `NET/S` → `EST-MB` → `JS-HEAP` → `PID` → `TAB-ID` → `TITLE`) |
| `o` / `S` | Toggle sort direction (`▼ DESC` ↔ `▲ ASC`) |
| `/` | Filter by title or URL (`Esc` clears filter) |
| `R` | Force immediate telemetry sync |
| `q` / `Ctrl+C` | Quit ChromeTop |


## 👤 Author
        
[Hadi Cahyadi](mailto:cumulus13@gmail.com)
    

[![Buy Me a Coffee](https://www.buymeacoffee.com/assets/img/custom_images/orange_img.png)](https://www.buymeacoffee.com/cumulus13)

[![Donate via Ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/cumulus13)
 
[Support me on Patreon](https://www.patreon.com/cumulus13)