# 🏗️ ChromeTop Architecture

## Overview

ChromeTop decouples browser telemetry collection and tab actuation from terminal rendering using a local JSON-over-WebSocket bridge:

1. **Chrome Extension (`extension/`):**
   - `background.js` (MV3 Service Worker) aggregates tab and window state via `chrome.tabs` and `chrome.windows`, queries page-level metrics from `content.js`, maintains a persistent WebSocket connection with auto-reconnect and keepalive alarms, and executes control commands (`FOCUS`, `CLOSE`, `RELOAD`, `DISCARD`, `MUTE`, `PIN`, `DUPLICATE`, `NEW_TAB`, `SYNC`).
   - `content.js` reads `performance.memory` (when exposed by Chrome) and DOM node counts on HTTP/HTTPS pages.
   - `popup.html` stores WebSocket endpoint and token settings in `chrome.storage.local`.
2. **Go Hub & TUI (`cmd/chrometop`, `internal/`):**
   - `internal/hub/server.go` hosts the WebSocket endpoint (`/ws`), validates optional auth tokens and `chrome-extension://` origins, runs ping/pong heartbeats, and exposes thread-safe snapshots via `sync.RWMutex`.
   - `internal/ui/tui.go` renders the Bubble Tea + Lip Gloss interface, handling navigation, filtering, sorting, tab inspection, clipboard copy, and command dispatch.

---

## Component & Data Flow Diagram

```mermaid
flowchart LR
    subgraph Chrome["Google Chrome (Manifest V3)"]
        TabsAPI["chrome.tabs & chrome.windows"]
        CS["content.js\n(JS Heap & DOM Nodes)"]
        SW["background.js\n(Service Worker)"]
        POP["popup.html\n(chrome.storage.local)"]

        TabsAPI -->|Tab & Window Events| SW
        CS -->|Page Metrics| SW
        POP -->|Endpoint & Token| SW
    end

    subgraph GoRuntime["ChromeTop Go Binary"]
        HUB["internal/hub/server.go\n(WebSocket Hub + RWMutex)"]
        TUI["internal/ui/tui.go\n(Bubble Tea + Lip Gloss)"]

        HUB -->|Thread-Safe Snapshot| TUI
        TUI -->|CommandMsg / Clipboard| HUB
    end

    SW <==>|"ws://127.0.0.1:9333/ws\n(TELEMETRY / COMMAND / ACK)"| HUB
```

---

## Command & Telemetry Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    participant CS as content.js
    participant SW as background.js (SW)
    participant Hub as Go WebSocket Hub
    participant TUI as Bubble Tea TUI

    SW->>Hub: Connect /ws?token=...
    Hub-->>SW: WebSocket Established

    loop Every 1s or on Tab Change
        SW->>CS: GET_PAGE_METRICS
        CS-->>SW: {jsHeapUsedMb, jsHeapTotalMb, domNodes}
        SW->>Hub: TELEMETRY (summary + tabs)
        Hub->>TUI: TickMsg
        TUI->>Hub: Snapshot()
        Hub-->>TUI: Latest Snapshot
    end

    TUI->>Hub: SendCommand(DUPLICATE, tabId)
    Hub->>SW: COMMAND {"action":"DUPLICATE","tabId":12}
    SW->>SW: chrome.tabs.duplicate(12)
    SW->>Hub: ACK {"ok":true,"detail":"Duplicated tab 12 -> 19"}
    SW->>Hub: TELEMETRY (updated tab list)
```
