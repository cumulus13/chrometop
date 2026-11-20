# 🏗️ ChromeTop Architecture

## Dual-Mode Telemetry & Control Design

ChromeTop supports both **Extension Mode** (WebSocket server in Go, client in Chromium/Brave/Chrome MV3 extension) and **Direct CDP Mode** (`--cdp`, WebSocket client in Go connecting to Chromium's `--remote-debugging-port` endpoints). Both backends feed the same thread-safe `hub.Server` state snapshot consumed by the Bubble Tea TUI.

---

## Architecture Diagram

```mermaid
flowchart LR
    subgraph ExtMode["Mode A: Chromium / Brave / Chrome Extension"]
        TabsAPI["chrome.tabs & chrome.processes"]
        WebReq["chrome.webRequest"]
        CS["content.js
(LongTask CPU, Heap, DOM)"]
        SW["background.js
(MV3 Service Worker)"]

        TabsAPI --> SW
        WebReq --> SW
        CS --> SW
    end

    subgraph CDPMode["Mode B: Direct CDP (--remote-debugging-port=9222)"]
        JSONList["HTTP /json/list & /json/version"]
        PageWS["Page WebSockets
(Performance.getMetrics)"]
        CDPClient["internal/cdp/client.go"]

        JSONList --> CDPClient
        PageWS --> CDPClient
    end

    subgraph GoCore["ChromeTop Go Core"]
        HUB["internal/hub/server.go
(Thread-Safe State & Command Router)"]
        TUI["internal/ui/tui.go
(Bubble Tea + Lip Gloss TUI)"]

        HUB <--> TUI
    end

    SW <==>|"ws://127.0.0.1:9333/ws"| HUB
    CDPClient <==>|"Direct Snapshot & Dispatch"| HUB
```

---

## Data Flow: Direct CDP Mode (`--cdp`)

```mermaid
sequenceDiagram
    autonumber
    participant TUI as Bubble Tea TUI
    participant Hub as hub.Server
    participant CDP as cdp.Client
    participant Browser as Chromium / Brave (:9222)

    loop Every 1s (--interval)
        CDP->>Browser: GET /json/list
        Browser-->>CDP: Page targets + webSocketDebuggerUrl
        CDP->>Browser: WS Performance.getMetrics & Runtime.evaluate
        Browser-->>CDP: TaskDuration, JSHeapUsedSize, Nodes, transferSize
        CDP->>Hub: UpdateDirectState(Summary, Tabs)
        Hub->>TUI: TickMsg
    end

    Note over TUI: User presses 's' (Sort) & 'o' (ASC/DESC)
    TUI->>TUI: Re-sort snapshot by CPU% / NET/S / MEM
```
