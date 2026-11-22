const DEFAULT_WS_ENDPOINT = "ws://127.0.0.1:9333/ws";
const PUSH_INTERVAL_MS = 1000;
const RETRY_DELAY_MS = 2000;

let ws = null;
let pushTimer = null;
let retryTimer = null;
let lastTelemetryTime = Date.now();

const pageMetricsCache = new Map();
const nativeProcessMap = new Map();
const tabNetworkBytes = new Map();
const tabPrevNetSnapshot = new Map();

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

// Native chrome.processes listener (active on Chromium Dev/Canary or enterprise-enabled builds)
function initNativeProcessMonitor() {
  if (chrome.processes && chrome.processes.onUpdatedWithMemory) {
    chrome.processes.onUpdatedWithMemory.addListener((processes) => {
      nativeProcessMap.clear();
      for (const proc of Object.values(processes)) {
        if (!Array.isArray(proc.tasks)) continue;
        for (const task of proc.tasks) {
          if (typeof task.tabId === "number" && task.tabId >= 0) {
            nativeProcessMap.set(task.tabId, {
              processId: proc.osProcessId || proc.id || 0,
              cpuPercent: typeof proc.cpu === "number" ? Number(proc.cpu.toFixed(1)) : 0,
              memoryBytes: proc.privateMemory || proc.jsMemoryUsed || 0,
              networkBytesPerSec: typeof proc.network === "number" ? proc.network : 0
            });
          }
        }
      }
    });
  }
}

// Per-tab network byte counter via webRequest headers
if (chrome.webRequest && chrome.webRequest.onCompleted) {
  chrome.webRequest.onCompleted.addListener(
    (details) => {
      if (typeof details.tabId !== "number" || details.tabId < 0) return;
      let bytes = 0;
      if (Array.isArray(details.responseHeaders)) {
        for (const h of details.responseHeaders) {
          if (h.name && h.name.toLowerCase() === "content-length" && h.value) {
            const parsed = parseInt(h.value, 10);
            if (!Number.isNaN(parsed) && parsed > 0) {
              bytes = parsed;
            }
            break;
          }
        }
      }
      if (bytes === 0) bytes = 2048; // Minimum header/frame overhead estimate
      tabNetworkBytes.set(details.tabId, (tabNetworkBytes.get(details.tabId) || 0) + bytes);
    },
    { urls: ["<all_urls>"] },
    ["responseHeaders"]
  );
}

async function queryPageMetrics(tab) {
  const cached = pageMetricsCache.get(tab.id) || {
    cpuPercent: 0,
    jsHeapUsedMb: 0,
    jsHeapTotalMb: 0,
    resourceTransferBytes: 0,
    domNodes: 0
  };

  if (tab.discarded || !tab.url || (!tab.url.startsWith("http://") && !tab.url.startsWith("https://"))) {
    return cached;
  }

  try {
    const res = await Promise.race([
      chrome.tabs.sendMessage(tab.id, { type: "GET_PAGE_METRICS" }),
      new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), 250))
    ]);
    if (res && typeof res.jsHeapUsedMb === "number") {
      pageMetricsCache.set(tab.id, res);
      return res;
    }
  } catch {
    // Content script unavailable or timed out on busy/restricted page
  }

  return cached;
}

function estimateTabFootprintMB(tab, heapUsedMB, nativeMemBytes) {
  if (nativeMemBytes && nativeMemBytes > 0) {
    return Number((nativeMemBytes / (1024 * 1024)).toFixed(1));
  }
  if (tab.discarded) return 2.5;
  let mb = 32.0 + (heapUsedMB || 0);
  if (tab.status === "loading") mb += 20.0;
  if (tab.audible) mb += 25.0;
  return Number(mb.toFixed(1));
}

async function buildTelemetryMessage() {
  const now = Date.now();
  const dtSec = Math.max(0.25, (now - lastTelemetryTime) / 1000);
  lastTelemetryTime = now;

  const [tabs, windows] = await Promise.all([
    chrome.tabs.query({}),
    chrome.windows.getAll({})
  ]);

  let activeCount = 0;
  let audibleCount = 0;
  let mutedCount = 0;
  let discardedCount = 0;
  let pinnedCount = 0;
  let totalCPU = 0;
  let totalNetRate = 0;
  let totalJSHeapMB = 0;
  let totalEstMB = 0;

  const formattedTabs = await Promise.all(
    tabs.map(async (tab) => {
      const muted = Boolean(tab.mutedInfo && tab.mutedInfo.muted);
      const pageMetrics = await queryPageMetrics(tab);
      const nativeProc = nativeProcessMap.get(tab.id);

      const processId = nativeProc ? nativeProc.processId : 0;
      const cpuPercent = nativeProc && nativeProc.cpuPercent > 0
        ? nativeProc.cpuPercent
        : (pageMetrics.cpuPercent || (tab.status === "loading" ? 8.5 : (tab.audible && !muted ? 4.2 : 0)));

      const trackedNet = Math.max(
        tabNetworkBytes.get(tab.id) || 0,
        pageMetrics.resourceTransferBytes || 0
      );
      const prevNet = tabPrevNetSnapshot.get(tab.id) || trackedNet;
      tabPrevNetSnapshot.set(tab.id, trackedNet);

      const netRate = nativeProc && nativeProc.networkBytesPerSec > 0
        ? nativeProc.networkBytesPerSec
        : Math.max(0, (trackedNet - prevNet) / dtSec);

      const estMB = estimateTabFootprintMB(
        tab,
        pageMetrics.jsHeapUsedMb,
        nativeProc ? nativeProc.memoryBytes : 0
      );

      if (tab.active) activeCount++;
      if (tab.audible && !muted) audibleCount++;
      if (muted) mutedCount++;
      if (tab.discarded) discardedCount++;
      if (tab.pinned) pinnedCount++;

      totalCPU += cpuPercent;
      totalNetRate += netRate;
      totalJSHeapMB += pageMetrics.jsHeapUsedMb || 0;
      totalEstMB += estMB;

      return {
        id: tab.id,
        windowId: tab.windowId,
        index: tab.index,
        processId,
        title: tab.title || "(Untitled Tab)",
        url: tab.url || "",
        status: tab.status || "complete",
        active: Boolean(tab.active),
        pinned: Boolean(tab.pinned),
        audible: Boolean(tab.audible),
        muted,
        discarded: Boolean(tab.discarded),
        autoDiscardable: Boolean(tab.autoDiscardable),
        cpuPercent: Number(cpuPercent.toFixed(1)),
        netBytesPerSec: Number(netRate.toFixed(1)),
        netTotalBytes: trackedNet,
        jsHeapUsedMb: pageMetrics.jsHeapUsedMb || 0,
        jsHeapTotalMb: pageMetrics.jsHeapTotalMb || 0,
        domNodes: pageMetrics.domNodes || 0,
        estimatedMb: estMB
      };
    })
  );

  return {
    type: "TELEMETRY",
    sentAt: now,
    summary: {
      mode: "EXTENSION",
      totalTabs: tabs.length,
      totalWindows: windows.length,
      activeTabs: activeCount,
      audibleTabs: audibleCount,
      mutedTabs: mutedCount,
      discardedTabs: discardedCount,
      pinnedTabs: pinnedCount,
      totalCpuPercent: Number(totalCPU.toFixed(1)),
      totalNetBytesPerSec: Number(totalNetRate.toFixed(1)),
      totalJsHeapMb: Number(totalJSHeapMB.toFixed(1)),
      estimatedTotalMb: Number(totalEstMB.toFixed(1)),
      userAgent: navigator.userAgent,
      platform: navigator.platform || "unknown"
    },
    tabs: formattedTabs
  };
}

// Tab events (title changes, loading state, ...) can fire in bursts. Each push
// queries every tab, so pushes are coalesced: at most one runs at a time, at
// most one more is queued behind it, and consecutive pushes are spaced by
// MIN_PUSH_GAP_MS. This keeps the browser and the ChromeTop UI responsive.
const MIN_PUSH_GAP_MS = 250;
let pushInFlight = false;
let pushQueued = false;
let lastPushAt = 0;

async function pushTelemetry() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  if (pushInFlight) {
    pushQueued = true;
    return;
  }

  pushInFlight = true;
  try {
    const wait = MIN_PUSH_GAP_MS - (Date.now() - lastPushAt);
    if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait));
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const msg = await buildTelemetryMessage();
    ws.send(JSON.stringify(msg));
    lastPushAt = Date.now();
  } catch (err) {
    console.error("[ChromeTop] Failed to send telemetry:", err);
  } finally {
    pushInFlight = false;
    if (pushQueued) {
      pushQueued = false;
      pushTelemetry();
    }
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
        tabNetworkBytes.delete(tabId);
        tabPrevNetSnapshot.delete(tabId);
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
  tabNetworkBytes.delete(tabId);
  tabPrevNetSnapshot.delete(tabId);
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

initNativeProcessMonitor();
connectSocket();
