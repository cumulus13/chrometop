package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
	"github.com/gorilla/websocket"
)

// newFakeChromium starts a minimal DevTools endpoint exposing two page targets
// ("A" and "B"). Pages listed in stuck accept requests but never answer them,
// which simulates a renderer whose main thread is blocked.
func newFakeChromium(t *testing.T, stuck map[string]bool) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()

	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":    "FakeChrome/1.0",
			"User-Agent": "fake-agent",
		})
	})

	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]string
		for _, id := range []string{"A", "B"} {
			list = append(list, map[string]string{
				"id":                   id,
				"type":                 "page",
				"title":                "Tab " + id,
				"url":                  "https://" + strings.ToLower(id) + ".test/",
				"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/page/" + id,
			})
		}
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/devtools/page/")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		calls := 0
		for {
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if stuck[id] {
				continue
			}

			var result any = map[string]any{}
			switch req.Method {
			case "Performance.getMetrics":
				calls++
				result = map[string]any{
					"metrics": []map[string]any{
						{"name": "TaskDuration", "value": float64(calls) * 0.1},
						{"name": "JSHeapUsedSize", "value": 20.0 * 1024 * 1024},
						{"name": "Nodes", "value": 100},
					},
				}
			case "Runtime.evaluate":
				result = map[string]any{
					"result": map[string]any{"type": "number", "value": 4096},
				}
			}
			if err := conn.WriteJSON(map[string]any{"id": req.ID, "result": result}); err != nil {
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPollPublishesTabsAndMetrics(t *testing.T) {
	srv := newFakeChromium(t, nil)
	h := hub.New("", nil)
	c := NewClient(srv.URL, h)
	t.Cleanup(c.closeAll)

	ctx := context.Background()
	c.pollOnce(ctx)
	time.Sleep(150 * time.Millisecond)
	c.pollOnce(ctx)

	snap := h.Snapshot()
	if !snap.Connected {
		t.Fatal("expected hub to be connected")
	}
	if len(snap.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(snap.Tabs))
	}
	if snap.Summary.Platform != "FakeChrome/1.0" {
		t.Fatalf("unexpected browser in summary: %q", snap.Summary.Platform)
	}

	for _, tab := range snap.Tabs {
		if tab.ID <= 0 {
			t.Fatalf("tab has invalid id: %+v", tab)
		}
		if tab.JSHeapUsedMB < 19 || tab.JSHeapUsedMB > 21 {
			t.Fatalf("unexpected heap for %s: %v", tab.Title, tab.JSHeapUsedMB)
		}
		if tab.CPUPercent <= 0 {
			t.Fatalf("expected CPU > 0 after two samples for %s, got %v", tab.Title, tab.CPUPercent)
		}
		if tab.NetTotalBytes != 4096 {
			t.Fatalf("unexpected net total for %s: %d", tab.Title, tab.NetTotalBytes)
		}
	}
}

func TestStuckTabDoesNotBlockPoll(t *testing.T) {
	srv := newFakeChromium(t, map[string]bool{"B": true})
	h := hub.New("", nil)
	c := NewClient(srv.URL, h)
	c.sampleTimeout = 300 * time.Millisecond
	c.cycleBudget = time.Second
	t.Cleanup(c.closeAll)

	start := time.Now()
	c.pollOnce(context.Background())
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("poll took %v; a stuck page must not stall the cycle", elapsed)
	}

	snap := h.Snapshot()
	if len(snap.Tabs) != 2 {
		t.Fatalf("expected both tabs to be reported, got %d", len(snap.Tabs))
	}

	byTitle := make(map[string]protocol.TabInfo)
	for _, tab := range snap.Tabs {
		if tab.ID <= 0 {
			t.Fatalf("tab has invalid id: %+v", tab)
		}
		byTitle[tab.Title] = tab
	}
	if byTitle["Tab A"].JSHeapUsedMB == 0 {
		t.Fatal("healthy tab must still report metrics")
	}
	if _, ok := byTitle["Tab B"]; !ok {
		t.Fatal("stuck tab must still be listed")
	}
}

func TestDispatchDoesNotPollSynchronously(t *testing.T) {
	srv := newFakeChromium(t, nil)
	h := hub.New("", nil)
	c := NewClient(srv.URL, h)
	t.Cleanup(c.closeAll)

	// SYNC must only queue a refresh request and return immediately.
	start := time.Now()
	if err := c.Dispatch(protocol.CommandMsg{Action: "SYNC"}); err != nil {
		t.Fatalf("dispatch SYNC: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("SYNC dispatch should not block")
	}

	select {
	case <-c.kickCh:
	default:
		t.Fatal("expected a pending refresh request")
	}

	// A second kick while one is pending must coalesce instead of blocking.
	c.kick()
	c.kick()
	if len(c.kickCh) != 1 {
		t.Fatalf("expected coalesced kick, queue length %d", len(c.kickCh))
	}
}

func TestOfflineOnlyAfterRepeatedFailures(t *testing.T) {
	srv := newFakeChromium(t, nil)
	h := hub.New("", nil)
	c := NewClient(srv.URL, h)
	t.Cleanup(c.closeAll)

	c.pollOnce(context.Background())
	if len(h.Snapshot().Tabs) != 2 {
		t.Fatal("expected tabs after first successful poll")
	}

	// Point the client at a dead endpoint.
	srv.Close()
	c.httpClient.Timeout = 200 * time.Millisecond

	for i := 1; i < offlineAfterFailures; i++ {
		c.pollOnce(context.Background())
		snap := h.Snapshot()
		if !snap.Connected || len(snap.Tabs) != 2 {
			t.Fatalf("failure %d: last known tabs must stay visible", i)
		}
	}

	c.pollOnce(context.Background())
	snap := h.Snapshot()
	if snap.Connected || len(snap.Tabs) != 0 {
		t.Fatalf("expected offline state after %d failures", offlineAfterFailures)
	}
}
