package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cumulus13/chrometop/internal/protocol"
	"github.com/gorilla/websocket"
)

func TestWebSocketTelemetryAndCommand(t *testing.T) {
	updated := make(chan struct{}, 8)
	srv := New("secret-tok", func() {
		select {
		case updated <- struct{}{}:
		default:
		}
	})

	httpSrv := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "?token=secret-tok"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	payload := protocol.TelemetryMsg{
		Type:   "TELEMETRY",
		SentAt: time.Now().UnixMilli(),
		Summary: protocol.Summary{
			TotalTabs:      2,
			TotalWindows:   1,
			TotalJSHeapMB:  42.5,
			EstimatedTotal: 110.0,
		},
		Tabs: []protocol.TabInfo{
			{ID: 10, WindowID: 1, Title: "Example Domain", URL: "https://example.com", EstimatedMB: 60.0, JSHeapUsedMB: 22.5},
			{ID: 11, WindowID: 1, Title: "GitHub", URL: "https://github.com", EstimatedMB: 50.0, JSHeapUsedMB: 20.0},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write telemetry: %v", err)
	}

	select {
	case <-updated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hub state update")
	}

	snap := srv.Snapshot()
	if !snap.Connected {
		t.Fatal("expected hub to report connected=true")
	}
	if len(snap.Tabs) != 2 || snap.Summary.TotalJSHeapMB != 42.5 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	cmd := protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    "RELOAD",
		TabID:     10,
		RequestID: "req-1",
	}
	if err := srv.SendCommand(cmd); err != nil {
		t.Fatalf("send command: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, incoming, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read command from server: %v", err)
	}

	var got protocol.CommandMsg
	if err := json.Unmarshal(incoming, &got); err != nil {
		t.Fatalf("unmarshal command: %v", err)
	}
	if got.Action != "RELOAD" || got.TabID != 10 {
		t.Fatalf("unexpected command received: %+v", got)
	}

	_ = context.Background()
}

func TestUnauthorizedTokenRejected(t *testing.T) {
	srv := New("expected-token", nil)
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "?token=wrong-token"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial error for invalid token")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %d", resp.StatusCode)
	}
}
