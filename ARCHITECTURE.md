# 🏗️ ChromeTop Architecture

ChromeTop has two interchangeable telemetry backends that feed one thread-safe state store (the **hub**), which the Bubble Tea UI renders.

## 1. Component overview

```mermaid
flowchart LR
    subgraph ExtMode["Mode A: Browser extension"]
        TabsAPI["chrome.tabs<br/>chrome.processes"]
        WebReq["chrome.webRequest"]
        CS["content.js<br/>LongTask CPU, heap, DOM"]
        SW["background.js<br/>MV3 service worker"]
        TabsAPI --> SW
        WebReq --> SW
        CS --> SW
    end

    subgraph CDPMode["Mode B: Direct CDP"]
        JSONList["HTTP /json/list"]
        PageWS["Per-page WebSocket<br/>Performance.getMetrics"]
        CDPClient["internal/cdp"]
        JSONList --> CDPClient
        PageWS --> CDPClient
    end

    subgraph Core["ChromeTop core"]
        HUB["internal/hub<br/>state + change signal"]
        TUI["internal/ui<br/>Bubble Tea + Lip Gloss"]
        HUB -- "Subscribe signal" --> TUI
        TUI -- "SendCommand in tea.Cmd" --> HUB
    end

    SW <== "ws://127.0.0.1:9333/ws" ==> HUB
    CDPClient -- "UpdateDirectState" --> HUB
    HUB -- "Dispatch" --> CDPClient
```

## 2. Package dependencies

```mermaid
flowchart TD
    main["cmd/chrometop"] --> ui["internal/ui"]
    main --> cdp["internal/cdp"]
    main --> hub["internal/hub"]
    ui --> hub
    ui --> protocol["internal/protocol"]
    cdp --> hub
    cdp --> protocol
    hub --> protocol
```

`protocol` has no dependencies. `hub` knows nothing about the UI or CDP: it only stores state and signals subscribers.

## 3. CDP polling cycle

One goroutine (`Client.Run`) owns polling, so cycles never overlap. Each cycle has a hard time budget, and pages that do not answer in time keep their last known values.

```mermaid
sequenceDiagram
    autonumber
    participant Run as cdp.Client.Run
    participant Browser as Chromium :9222
    participant Sess as session (per page)
    participant Hub as hub.Server
    participant UI as Bubble Tea

    loop every interval or on kick
        Run->>Browser: GET /json/list
        Browser-->>Run: page targets
        par up to 8 workers, within cycle budget
            Run->>Sess: sample()
            Sess->>Browser: Performance.getMetrics + Runtime.evaluate (pipelined)
            Browser-->>Sess: responses via reader goroutine
        end
        Run->>Hub: UpdateDirectState(tabs, summary)
        Hub-->>UI: coalesced change signal
        UI->>Hub: Snapshot() once
    end
```

### Session lifecycle

```mermaid
stateDiagram-v2
    [*] --> Idle
    Idle --> Connecting: first sample
    Connecting --> Ready: handshake ok
    Connecting --> Failing: dial error
    Ready --> Ready: sample ok
    Ready --> Failing: timeout or error
    Failing --> Ready: next sample ok
    Failing --> Backoff: 3 consecutive failures
    Backoff --> Idle: back-off elapsed (up to 10 s)
    Ready --> [*]: tab closed
```

## 4. Extension telemetry and commands

```mermaid
sequenceDiagram
    autonumber
    participant Tab as Browser tabs
    participant SW as background.js
    participant Hub as hub.Server
    participant UI as TUI

    Tab->>SW: tab events (coalesced, min 250 ms apart)
    SW->>SW: build telemetry (query tabs and content scripts)
    SW->>Hub: TELEMETRY frame
    Hub-->>UI: change signal
    UI->>Hub: SendCommand (inside tea.Cmd goroutine)
    Hub->>SW: COMMAND frame
    SW->>Tab: chrome.tabs.* action
    SW->>Hub: ACK frame
    Hub-->>UI: change signal (event log)
```

## 5. UI event loop

The model is updated only by messages. Nothing in `Update` or `View` blocks.

```mermaid
flowchart TD
    Start(["Init"]) --> W["waitForChange: wait on hub signal"]
    Start --> T["tickCmd: 1 s clock"]

    W -- "hubChangedMsg" --> Snap["m.snap = hub.Snapshot()<br/>refreshRows: filter, sort, keep selection"]
    Snap --> W

    T -- "tickMsg" --> Redraw["redraw only"]
    Redraw --> T

    Key["tea.KeyMsg"] --> Nav["navigation, sort, filter: pure model update"]
    Key --> Cmd["tab action: returns tea.Cmd"]
    Cmd --> Go["goroutine: hub.SendCommand"]
    Go -- "bannerMsg on error" --> Banner["footer banner"]

    Resize["tea.WindowSizeMsg"] --> Layout["store size, clamp scroll"]
```

## 6. Layout engine

The terminal height is split exactly, so no rows are left over.

```mermaid
flowchart TB
    A["Top panel: 4 rows<br/>border + 2 summary lines"]
    B["Filter row: 1 row"]
    C["Column header: 1 row"]
    D["Table body: height - 6 - footer rows<br/>padded to the exact size"]
    E["Footer: 1 status row + 1 or 2 key-hint rows"]
    A --> B --> C --> D --> E
```

Rules that keep the frame exactly `height` rows tall and at most `width` cells wide:

- Footer height comes from the real wrapped key hints, not from a constant.
- Every line is truncated to the available width with an ANSI-aware, wide-character-aware function. Nothing wraps unexpectedly.
- Table cells are padded by **display width**. Emoji and CJK text count as two cells, so columns stay aligned.
- `fitHeight` pads or cuts the final frame to exactly `height` lines.

## 7. Why the UI used to freeze (and the fixes)

| Root cause | Effect | Fix |
| :--- | :--- | :--- |
| Hub change callback sent `TickMsg`, and every `TickMsg` scheduled a *new* 1 s tick | Tick chains multiplied with every update, so redraw rate grew over time | One tick chain; hub changes use a separate message and a coalescing channel |
| `Dispatch` (HTTP and WebSocket I/O, up to seconds) ran inside `Update` | UI froze on every action | Commands run inside `tea.Cmd` goroutines |
| `Dispatch` and the `SYNC` key ran a full poll synchronously | Multi-second stalls | They call `kick()`, and the poll loop refreshes asynchronously |
| Hub callback called `Program.Send` from the same goroutine that runs `Update` | Possible deadlock | Hub never calls into the UI; it only signals a buffered channel |
| `View` copied and re-sorted state on every frame | Wasted CPU with many tabs | Snapshot and sort once per hub change |
| Sequential page sampling with per-read deadlines on a shared socket | One slow page stalled the cycle and corrupted the socket | Reader goroutine per page, request IDs, per-cycle time budget, back-off |
| Any failed poll blanked the table | Flicker | Last known tabs stay until 3 consecutive failures |

## 8. Concurrency model

| Component | Goroutines | Shared state | Protection |
| :--- | :--- | :--- | :--- |
| Hub | WebSocket handler, heartbeat | tabs, summary, log, revision | `sync.RWMutex` |
| CDP client | `Run` loop, up to 8 samplers per cycle, one reader per page | sessions, tab IDs | `sync.Mutex` |
| CDP session | reader goroutine, sampler, dispatcher | connection, pending requests | `mu`, `writeMu`, `dialMu` |
| UI | Bubble Tea loop, short-lived command goroutines | model (value type) | confined to the loop |

## 9. Design decisions

- **Passive hub.** Backends push state in, the UI pulls it out. Neither side can block the other.
- **Coalescing notifications.** A buffered channel of capacity 1 means producers never block and bursts collapse into one redraw.
- **Reader goroutine per CDP page.** `gorilla/websocket` leaves a connection unusable after a read deadline expires. A dedicated reader with ID-matched responses removes that failure mode.
- **Graceful degradation.** Slow or stuck pages show stale values and back off instead of stalling everyone.
