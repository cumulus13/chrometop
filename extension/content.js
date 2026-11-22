let longTaskMs = 0;
let lastSampleTime = performance.now();

if (typeof PerformanceObserver !== "undefined") {
  try {
    const observer = new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        longTaskMs += entry.duration || 0;
      }
    });
    observer.observe({ entryTypes: ["longtask"] });
  } catch {
    // longtask observer not supported on this page
  }
}

function collectPageMetrics() {
  const now = performance.now();
  const elapsed = Math.max(100, now - lastSampleTime);
  const cpuApprox = Math.min(100, Number(((longTaskMs / elapsed) * 100).toFixed(1)));
  longTaskMs = 0;
  lastSampleTime = now;

  let usedMB = 0;
  let totalMB = 0;
  if (performance && performance.memory) {
    usedMB = Number((performance.memory.usedJSHeapSize / (1024 * 1024)).toFixed(2));
    totalMB = Number((performance.memory.totalJSHeapSize / (1024 * 1024)).toFixed(2));
  }

  let resourceTransferBytes = 0;
  try {
    const entries = performance.getEntriesByType("resource");
    for (let i = 0; i < entries.length; i++) {
      resourceTransferBytes += entries[i].transferSize || 0;
    }
  } catch {
    // Ignore cross-origin resource timing restrictions
  }

  const domNodes = document.getElementsByTagName("*").length;

  return {
    cpuPercent: cpuApprox,
    jsHeapUsedMb: usedMB,
    jsHeapTotalMb: totalMB,
    resourceTransferBytes,
    domNodes
  };
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg && msg.type === "GET_PAGE_METRICS") {
    try {
      sendResponse(collectPageMetrics());
    } catch {
      sendResponse({ cpuPercent: 0, jsHeapUsedMb: 0, jsHeapTotalMb: 0, resourceTransferBytes: 0, domNodes: 0 });
    }
  }
  // The response is sent synchronously, so the channel must not be kept open.
  return false;
});
