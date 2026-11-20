package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
	"github.com/gorilla/websocket"
)

type cdpTarget struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpVersion struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type persistentSession struct {
	wsURL        string
	conn         *websocket.Conn
	writeMu      sync.Mutex
	enabled      bool
	lastCPUTime  float64
	lastSampleAt time.Time
	lastNetBytes uint64

	// Cached last known metrics so UI never flickers to zero
	cpuPercent  float64
	netRate     float64
	netTotal    uint64
	heapUsedMB  float64
	heapTotalMB float64
	domNodes    int
}

func (s *persistentSession) close() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
		s.enabled = false
	}
}

// Client connects directly to Chromium/Chrome/Brave via --remote-debugging-port.
type Client struct {
	baseURL    string
	httpClient *http.Client
	hub        *hub.Server

	mu       sync.Mutex
	sessions map[string]*persistentSession
	tabIDs   map[string]int
	nextID   int
}

// NewClient initializes a direct CDP collector and command dispatcher.
func NewClient(baseURL string, h *hub.Server) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}
	return &Client{
		baseURL: trimmed,
		httpClient: &http.Client{
			Timeout: 1200 * time.Millisecond,
		},
		hub:      h,
		sessions: make(map[string]*persistentSession),
		tabIDs:   make(map[string]int),
		nextID:   1,
	}
}

// Run polls CDP /json/list and collects metrics over persistent per-page WebSockets.
func (c *Client) Run(ctx context.Context, interval time.Duration) {
	if interval < 300*time.Millisecond {
		interval = 1 * time.Second
	}
	c.pollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	defer func() {
		c.mu.Lock()
		for _, s := range c.sessions {
			s.close()
		}
		c.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollOnce(ctx)
		}
	}
}

func (c *Client) pollOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	ver, err := c.fetchVersion(ctx)
	if err != nil {
		c.hub.UpdateDirectState(
			false,
			c.baseURL,
			protocol.Summary{Mode: "CDP"},
			nil,
			fmt.Sprintf("🔴 CDP unreachable at %s (%v)", c.baseURL, err),
		)
		return
	}

	targets, err := c.fetchTargets(ctx)
	if err != nil {
		c.hub.UpdateDirectState(
			false,
			c.baseURL,
			protocol.Summary{Mode: "CDP"},
			nil,
			fmt.Sprintf("🔴 CDP /json/list failed: %v", err),
		)
		return
	}

	var pageTargets []cdpTarget
	seen := make(map[string]bool)
	for _, tgt := range targets {
		if tgt.Type == "page" {
			pageTargets = append(pageTargets, tgt)
			seen[tgt.ID] = true
		}
	}

	// Clean up closed tabs
	c.mu.Lock()
	for id, sess := range c.sessions {
		if !seen[id] {
			sess.close()
			delete(c.sessions, id)
			delete(c.tabIDs, id)
		}
	}
	c.mu.Unlock()

	tabs := make([]protocol.TabInfo, len(pageTargets))
	const maxWorkers = 8
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for idx, tgt := range pageTargets {
		numericID := c.numericIDFor(tgt.ID)
		sess := c.getOrCreateSession(tgt.ID, tgt.WebSocketDebuggerURL)

		wg.Add(1)
		go func(i int, id int, target cdpTarget, session *persistentSession, isFirst bool) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				c.sampleSession(ctx, session)
				<-sem
			case <-ctx.Done():
				return
			}

			estMB := 35.0 + session.heapUsedMB
			if session.heapUsedMB == 0 {
				estMB = 48.0
			}

			tabs[i] = protocol.TabInfo{
				ID:             id,
				TargetID:       target.ID,
				WindowID:       1,
				Index:          id, // Stable index prevents row jumping across polls
				Title:          fallbackTitle(target.Title),
				URL:            target.URL,
				Status:         "complete",
				Active:         isFirst,
				CPUPercent:     round1(session.cpuPercent),
				NetBytesPerSec: round1(session.netRate),
				NetTotalBytes:  session.netTotal,
				JSHeapUsedMB:   round2(session.heapUsedMB),
				JSHeapTotalMB:  round2(session.heapTotalMB),
				DOMNodes:       session.domNodes,
				EstimatedMB:    round1(estMB),
			}
		}(idx, numericID, tgt, sess, idx == 0)
	}
	wg.Wait()

	// Keep deterministic base order by numeric tab ID so ties never shuffle rows
	sort.Slice(tabs, func(i, j int) bool {
		return tabs[i].ID < tabs[j].ID
	})

	// Single atomic state update per cycle (eliminates visual flickering/rolling)
	c.hub.UpdateDirectState(true, c.baseURL, c.buildSummary(ver, tabs), tabs, "")
}

func (c *Client) getOrCreateSession(targetID, wsURL string) *persistentSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[targetID]; ok {
		if wsURL != "" && s.wsURL != wsURL {
			s.close()
			s.wsURL = wsURL
		}
		return s
	}
	s := &persistentSession{wsURL: wsURL}
	c.sessions[targetID] = s
	return s
}

func (c *Client) sampleSession(ctx context.Context, s *persistentSession) {
	if s.wsURL == "" {
		return
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.conn == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		dialer := websocket.Dialer{HandshakeTimeout: 250 * time.Millisecond}
		conn, _, err := dialer.DialContext(dialCtx, s.wsURL, nil)
		cancel()
		if err != nil {
			return
		}
		s.conn = conn
		s.enabled = false
	}

	deadline := time.Now().Add(350 * time.Millisecond)
	_ = s.conn.SetReadDeadline(deadline)
	_ = s.conn.SetWriteDeadline(deadline)

	if !s.enabled {
		if err := s.conn.WriteJSON(map[string]any{"id": 1, "method": "Performance.enable"}); err != nil {
			s.closeLocked()
			return
		}
		s.enabled = true
	}

	if err := s.conn.WriteJSON(map[string]any{"id": 2, "method": "Performance.getMetrics"}); err != nil {
		s.closeLocked()
		return
	}

	if err := s.conn.WriteJSON(map[string]any{
		"id":     3,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    "(() => { try { return performance.getEntriesByType('resource').reduce((a, b) => a + (b.transferSize || 0), 0); } catch (e) { return 0; } })()",
			"returnByValue": true,
		},
	}); err != nil {
		s.closeLocked()
		return
	}

	var taskDuration float64
	var threadTime float64
	gotMetrics := false
	gotEval := false

	for i := 0; i < 10 && (!gotMetrics || !gotEval); i++ {
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			// Reset broken socket so next tick reconnects cleanly
			s.closeLocked()
			break
		}
		var env struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || env.ID == 0 {
			continue
		}

		switch env.ID {
		case 2:
			var res struct {
				Metrics []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"metrics"`
			}
			if json.Unmarshal(env.Result, &res) == nil {
				for _, m := range res.Metrics {
					switch m.Name {
					case "TaskDuration":
						taskDuration = m.Value
					case "ThreadTime":
						threadTime = m.Value
					case "JSHeapUsedSize":
						s.heapUsedMB = m.Value / (1024 * 1024)
					case "JSHeapTotalSize":
						s.heapTotalMB = m.Value / (1024 * 1024)
					case "Nodes":
						s.domNodes = int(m.Value)
					}
				}
				gotMetrics = true
			}
		case 3:
			var evalRes struct {
				Result struct {
					Value float64 `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(env.Result, &evalRes) == nil {
				if evalRes.Result.Value > 0 {
					s.netTotal = uint64(evalRes.Result.Value)
				}
				gotEval = true
			}
		}
	}

	if !gotMetrics {
		return
	}

	cpuClock := taskDuration
	if cpuClock <= 0 && threadTime > 0 {
		cpuClock = threadTime
	}

	now := time.Now()
	if !s.lastSampleAt.IsZero() && cpuClock > 0 {
		dt := now.Sub(s.lastSampleAt).Seconds()
		if dt > 0.05 {
			dCPU := cpuClock - s.lastCPUTime
			if dCPU >= 0 {
				pct := (dCPU / dt) * 100.0
				if pct > 100.0 {
					pct = 100.0
				}
				s.cpuPercent = pct
			}
			if s.netTotal >= s.lastNetBytes {
				s.netRate = float64(s.netTotal-s.lastNetBytes) / dt
			}
		}
	}

	if cpuClock > 0 {
		s.lastCPUTime = cpuClock
	}
	s.lastNetBytes = s.netTotal
	s.lastSampleAt = now
}

func (s *persistentSession) closeLocked() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
		s.enabled = false
	}
}

func (c *Client) buildSummary(ver cdpVersion, tabs []protocol.TabInfo) protocol.Summary {
	var totalCPU, totalNet, totalHeap, totalEst float64
	for _, t := range tabs {
		totalCPU += t.CPUPercent
		totalNet += t.NetBytesPerSec
		totalHeap += t.JSHeapUsedMB
		totalEst += t.EstimatedMB
	}
	if totalCPU > 100.0 {
		totalCPU = 100.0
	}
	return protocol.Summary{
		Mode:           "CDP",
		TotalTabs:      len(tabs),
		TotalWindows:   1,
		ActiveTabs:     minOne(len(tabs)),
		TotalCPUPerc:   round1(totalCPU),
		TotalNetRate:   round1(totalNet),
		TotalJSHeapMB:  round1(totalHeap),
		EstimatedTotal: round1(totalEst),
		UserAgent:      ver.UserAgent,
		Platform:       ver.Browser,
	}
}

func (c *Client) numericIDFor(targetID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.tabIDs[targetID]; ok {
		return id
	}
	id := c.nextID
	c.nextID++
	c.tabIDs[targetID] = id
	return id
}

func (c *Client) fetchVersion(ctx context.Context) (cdpVersion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/json/version", nil)
	if err != nil {
		return cdpVersion{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return cdpVersion{}, err
	}
	defer resp.Body.Close()

	var v cdpVersion
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return cdpVersion{}, err
	}
	return v, nil
}

func (c *Client) fetchTargets(ctx context.Context) ([]cdpTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var targets []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// Dispatch executes tab control commands directly over Chromium CDP HTTP/WebSocket endpoints.
func (c *Client) Dispatch(cmd protocol.CommandMsg) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	targetID := cmd.TargetID
	if targetID == "" && cmd.TabID > 0 {
		for _, t := range c.hub.Snapshot().Tabs {
			if t.ID == cmd.TabID {
				targetID = t.TargetID
				break
			}
		}
	}

	switch cmd.Action {
	case "FOCUS":
		if targetID == "" {
			return fmt.Errorf("missing targetId for FOCUS")
		}
		if err := c.hitEndpoint(ctx, "/json/activate/"+url.PathEscape(targetID)); err != nil {
			return err
		}
		c.hub.SetStatusBanner(fmt.Sprintf("✅ CDP activated target %s", shortID(targetID)))

	case "CLOSE":
		if targetID == "" {
			return fmt.Errorf("missing targetId for CLOSE")
		}
		if err := c.hitEndpoint(ctx, "/json/close/"+url.PathEscape(targetID)); err != nil {
			return err
		}
		c.hub.SetStatusBanner(fmt.Sprintf("✅ CDP closed target %s", shortID(targetID)))

	case "NEW_TAB":
		targetURL := cmd.URL
		if targetURL == "" {
			targetURL = "about:blank"
		}
		endpoint := "/json/new?" + url.QueryEscape(targetURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		c.hub.SetStatusBanner("✅ CDP opened new tab")

	case "RELOAD":
		if err := c.sendPageCommand(ctx, targetID, "Page.reload", nil); err != nil {
			return err
		}
		c.hub.SetStatusBanner(fmt.Sprintf("✅ CDP reloaded target %s", shortID(targetID)))

	case "DUPLICATE":
		var pageURL string
		for _, t := range c.hub.Snapshot().Tabs {
			if t.TargetID == targetID {
				pageURL = t.URL
				break
			}
		}
		if pageURL == "" {
			pageURL = "about:blank"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/json/new?"+url.QueryEscape(pageURL), nil)
		if err != nil {
			return err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		c.hub.SetStatusBanner(fmt.Sprintf("✅ CDP duplicated target %s", shortID(targetID)))

	case "SYNC":
		c.pollOnce(ctx)
		c.hub.SetStatusBanner("✅ CDP telemetry synced")
		return nil

	default:
		return fmt.Errorf("action %s is supported in Extension mode (CDP supports FOCUS, RELOAD, DUPLICATE, NEW_TAB, CLOSE, SYNC)", cmd.Action)
	}

	c.pollOnce(ctx)
	return nil
}

func (c *Client) hitEndpoint(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("CDP HTTP %d on %s", resp.StatusCode, path)
	}
	return nil
}

func (c *Client) sendPageCommand(ctx context.Context, targetID, method string, params map[string]any) error {
	targets, err := c.fetchTargets(ctx)
	if err != nil {
		return err
	}
	var wsURL string
	for _, t := range targets {
		if t.ID == targetID {
			wsURL = t.WebSocketDebuggerURL
			break
		}
	}
	if wsURL == "" {
		return fmt.Errorf("target %s debugger URL not found", targetID)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(600 * time.Millisecond))
	msg := map[string]any{
		"id":     1,
		"method": method,
	}
	if params != nil {
		msg["params"] = params
	}
	return conn.WriteJSON(msg)
}

func fallbackTitle(t string) string {
	if strings.TrimSpace(t) == "" {
		return "(Untitled)"
	}
	return t
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func minOne(n int) int {
	if n > 0 {
		return 1
	}
	return 0
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10.0
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100.0
}
