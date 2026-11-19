const DEFAULT_WS_ENDPOINT = "ws://127.0.0.1:9333/ws";
const PUSH_INTERVAL_MS = 1000;
const RETRY_DELAY_MS = 2000;

let ws = null;
let pushTimer = null;
let retryTimer = null;
const pageMetricsCache = new Map();

async function readSettings() {
  const data = await chrome.storage.local.get(["wsEndpoint", "authToken"]);
  return {
    endpoint: (data.wsEndpoint || DEFAULT_WS_ENDPOINT).trim(),
    token: (data.authToken || "").trim()
  };
}

function makeSocketURL(endpoint, token) {
  if (!token) return endpoint;
  const sep = endpoint.includes("?") ? "&" : "?";
  return `${endpoint}${sep}token=${encodeURIComponent(token)}`;
}

async function queryPageMetrics(tab) {
  if (tab.discarded || !tab.url || (!tab.url.startsWith("http://") && !tab.url.startsWith("https://"))) {
    return pageMetricsCache.get(tab.id) || { jsHeapUsedMb: 0, jsHeapTotalMb: 0, domNodes: 0 };
  }

  try {
    const res = await chrome.tabs.sendMessage(tab.id, { type: "GET_PAGE_METRICS" });
    if (res && typeof res.jsHeapUsedMb === "number") {
      pageMetricsCache.set(tab.id, res);
      return res;
    }
  } catch {
    // Content script may not be injected yet on restricted or freshly opened pages
  }

  return pageMetricsCache.get(tab.id) || { jsHeapUsedMb: 0, jsHeapTotalMb: 0, domNodes: 0 };
}

function estimateTabFootprintMB(tab, heapUsedMB) {
  if (tab.discarded) return 2.5;
  let mb = 32.0 + (heapUsedMB || 0);
  if (tab.status === "loading") mb += 20.0;
  if (tab.audible) mb += 25.0;
  return Number(mb.toFixed(1));
}

async function buildTelemetryMessage() {
  const [tabs, windows] = await Promise.all([
    chrome.tabs.query({}),
    chrome.windows.getAll({})
  ]);

  let activeCount = 0;
  let audibleCount = 0;
  let mutedCount = 0;
  let discardedCount = 0;
  let pinnedCount = 0;
  let totalJSHeapMB = 0;
  let totalEstMB = 0;

  const formattedTabs = await Promise.all(
    tabs.map(async (tab) => {
      const muted = Boolean(tab.mutedInfo && tab.mutedInfo.muted);
      const metrics = await queryPageMetrics(tab);
      const estMB = estimateTabFootprintMB(tab, metrics.jsHeapUsedMb);

      if (tab.active) activeCount++;
      if (tab.audible && !muted) audibleCount++;
      if (muted) mutedCount++;
      if (tab.discarded) discardedCount++;
      if (tab.pinned) pinnedCount++;
      totalJSHeapMB += metrics.jsHeapUsedMb || 0;
      totalEstMB += estMB;

      return {
        id: tab.id,
        windowId: tab.windowId,
        index: tab.index,
        title: tab.title || "(Untitled Tab)",
        url: tab.url || "",
        status: tab.status || "complete",
        active: Boolean(tab.active),
        pinned: Boolean(tab.pinned),
        audible: Boolean(tab.audible),
        muted,
        discarded: Boolean(tab.discarded),
        autoDiscardable: Boolean(tab.autoDiscardable),
        jsHeapUsedMb: metrics.jsHeapUsedMb || 0,
        jsHeapTotalMb: metrics.jsHeapTotalMb || 0,
        domNodes: metrics.domNodes || 0,
        estimatedMb: estMB
      };
    })
  );

  return {
    type: "TELEMETRY",
    sentAt: Date.now(),
    summary: {
      totalTabs: tabs.length,
      totalWindows: windows.length,
      activeTabs: activeCount,
      audibleTabs: audibleCount,
      mutedTabs: mutedCount,
      discardedTabs: discardedCount,
      pinnedTabs: pinnedCount,
      totalJsHeapMb: Number(totalJSHeapMB.toFixed(1)),
      estimatedTotalMb: Number(totalEstMB.toFixed(1)),
      userAgent: navigator.userAgent,
      platform: navigator.platform || "unknown"
    },
    tabs: formattedTabs
  };
}

async function pushTelemetry() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  try {
    const msg = await buildTelemetryMessage();
    ws.send(JSON.stringify(msg));
  } catch (err) {
    console.error("[ChromeTop] Failed to send telemetry:", err);
  }
}

function sendAck(requestId, ok, detail) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({
    type: "ACK",
    requestId,
    ok,
    detail
  }));
}

async function runCommand(cmd) {
  const { action, tabId, windowId, url, requestId } = cmd;
  try {
    switch (action) {
      case "FOCUS":
        if (windowId) await chrome.windows.update(windowId, { focused: true });
        await chrome.tabs.update(tabId, { active: true });
        sendAck(requestId, true, `Focused tab ${tabId}`);
        break;

      case "CLOSE":
        pageMetricsCache.delete(tabId);
        await chrome.tabs.remove(tabId);
        sendAck(requestId, true, `Closed tab ${tabId}`);
        break;

      case "RELOAD":
        await chrome.tabs.reload(tabId);
        sendAck(requestId, true, `Reloaded tab ${tabId}`);
        break;

      case "DISCARD":
        await chrome.tabs.discard(tabId);
        sendAck(requestId, true, `Discarded tab ${tabId}`);
        break;

      case "MUTE": {
        const current = await chrome.tabs.get(tabId);
        const targetMuted = !(current.mutedInfo && current.mutedInfo.muted);
        await chrome.tabs.update(tabId, { muted: targetMuted });
        sendAck(requestId, true, `${targetMuted ? "Muted" : "Unmuted"} tab ${tabId}`);
        break;
      }

      case "PIN": {
        const current = await chrome.tabs.get(tabId);
        const nextPinned = !current.pinned;
        await chrome.tabs.update(tabId, { pinned: nextPinned });
        sendAck(requestId, true, `${nextPinned ? "Pinned" : "Unpinned"} tab ${tabId}`);
        break;
      }

      case "DUPLICATE": {
        const created = await chrome.tabs.duplicate(tabId);
        sendAck(requestId, true, `Duplicated tab ${tabId} -> ${created ? created.id : "new"}`);
        break;
      }

      case "NEW_TAB": {
        const created = await chrome.tabs.create({ url: url || undefined, active: false });
        sendAck(requestId, true, `Created new tab ${created.id}`);
        break;
      }

      case "SYNC":
        await pushTelemetry();
        sendAck(requestId, true, "Manual sync complete");
        return;

      default:
        sendAck(requestId, false, `Unsupported command: ${action}`);
        return;
    }

    await pushTelemetry();
  } catch (err) {
    sendAck(requestId, false, err.message || String(err));
  }
}

async function connectSocket() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const { endpoint, token } = await readSettings();
  const fullURL = makeSocketURL(endpoint, token);

  try {
    ws = new WebSocket(fullURL);
  } catch {
    scheduleRetry();
    return;
  }

  ws.onopen = async () => {
    await chrome.action.setBadgeText({ text: "LIVE" });
    await chrome.action.setBadgeBackgroundColor({ color: "#00FF9C" });
    await pushTelemetry();
    clearInterval(pushTimer);
    pushTimer = setInterval(pushTelemetry, PUSH_INTERVAL_MS);
  };

  ws.onmessage = async (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      if (msg.type === "COMMAND") {
        await runCommand(msg);
      }
    } catch (err) {
      console.error("[ChromeTop] Invalid command payload:", err);
    }
  };

  ws.onclose = async () => {
    await chrome.action.setBadgeText({ text: "OFF" });
    await chrome.action.setBadgeBackgroundColor({ color: "#FF5555" });
    clearInterval(pushTimer);
    scheduleRetry();
  };

  ws.onerror = () => {
    if (ws) ws.close();
  };
}

function scheduleRetry() {
  clearTimeout(retryTimer);
  retryTimer = setTimeout(connectSocket, RETRY_DELAY_MS);
}

chrome.alarms.create("chrometop-heartbeat", { periodInMinutes: 0.4 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "chrometop-heartbeat") {
    connectSocket();
  }
});

chrome.tabs.onCreated.addListener(pushTelemetry);
chrome.tabs.onRemoved.addListener((tabId) => {
  pageMetricsCache.delete(tabId);
  pushTelemetry();
});
chrome.tabs.onUpdated.addListener((_, info) => {
  if (
    info.status ||
    info.title ||
    info.audible !== undefined ||
    info.mutedInfo ||
    info.pinned !== undefined ||
    info.discarded !== undefined
  ) {
    pushTelemetry();
  }
});

connectSocket();
