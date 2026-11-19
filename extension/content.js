function collectPageMetrics() {
  let usedMB = 0;
  let totalMB = 0;

  if (performance && performance.memory) {
    usedMB = Number((performance.memory.usedJSHeapSize / (1024 * 1024)).toFixed(2));
    totalMB = Number((performance.memory.totalJSHeapSize / (1024 * 1024)).toFixed(2));
  }

  const domNodes = document.getElementsByTagName("*").length;

  return {
    jsHeapUsedMb: usedMB,
    jsHeapTotalMb: totalMB,
    domNodes
  };
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg && msg.type === "GET_PAGE_METRICS") {
    try {
      sendResponse(collectPageMetrics());
    } catch {
      sendResponse({ jsHeapUsedMb: 0, jsHeapTotalMb: 0, domNodes: 0 });
    }
  }
  return true;
});
