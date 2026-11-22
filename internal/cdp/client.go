// Package cdp implements ChromeTop's direct Chrome DevTools Protocol backend.
//
// It polls /json/list for page targets, samples every page over a persistent
// WebSocket and publishes one atomic state update per cycle to the hub.
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
)

const (
	// maxWorkers bounds the number of pages sampled in parallel.
	maxWorkers = 8

	// offlineAfterFailures is the number of consecutive failed polls after which
	// the hub is switched to the offline state. Until then the last known tabs
	// stay on screen so a single slow response does not blank the table.
	offlineAfterFailures = 3

	defaultSampleTimeout = 600 * time.Millisecond
	defaultCycleBudget   = 2 * time.Second
	versionTTL           = 5 * time.Minute
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

// Client connects directly to Chromium/Chrome/Brave via --remote-debugging-port.
type Client struct {
	baseURL    string
	httpClient *http.Client
	hub        *hub.Server

	// sampleTimeout bounds the time spent on one page per cycle.
	sampleTimeout time.Duration

	// cycleBudget bounds the time spent sampling all pages in one cycle.
	cycleBudget time.Duration

	// kickCh asks the Run loop for an immediate refresh without blocking the caller.
	kickCh chan struct{}

	mu       sync.Mutex // guards sessions, tabIDs and nextID
	sessions map[string]*session
	tabIDs   map[string]int
	nextID   int

	// Poll-loop state. Only touched by the goroutine running pollOnce.
	version   cdpVersion
	versionAt time.Time
	failures  int
	online    bool
}

// NewClient initializes a direct CDP collector and command dispatcher.
func NewClient(baseURL string, h *hub.Server) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}
	return &Client{
		baseURL:       trimmed,
		httpClient:    &http.Client{Timeout: 2 * time.Second},
		hub:           h,
		sampleTimeout: defaultSampleTimeout,
		cycleBudget:   defaultCycleBudget,
		kickCh:        make(chan struct{}, 1),
		sessions:      make(map[string]*session),
		tabIDs:        make(map[string]int),
		nextID:        1,
	}
}

// Run polls CDP until ctx is cancelled. It is the only goroutine that runs
// pollOnce, so cycles never overlap.
func (c *Client) Run(ctx context.Context, interval time.Duration) {
	if interval < 250*time.Millisecond {
		interval = time.Second
	}

	// Let a cycle use about 1.5x the interval, but never less than the default
	// budget so that slow machines still get a chance to finish a cycle.
	budget := interval * 3 / 2
	if budget < defaultCycleBudget {
		budget = defaultCycleBudget
	}
	c.cycleBudget = budget

	defer c.closeAll()

	c.pollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollOnce(ctx)
		case <-c.kickCh:
			c.pollOnce(ctx)
		}
	}
}

// kick requests an immediate refresh. It never blocks.
func (c *Client) kick() {
	select {
	case c.kickCh <- struct{}{}:
	default:
	}
}

func (c *Client) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessions {
		s.close()
	}
}

func (c *Client) pollOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	targets, err := c.fetchTargets(ctx)
	if err != nil {
		c.failures++
		if c.failures < offlineAfterFailures {
			c.hub.SetStatusBanner(fmt.Sprintf("⏳ CDP slow to respond (%d/%d): %v", c.failures, offlineAfterFailures, err))
			return
		}
		c.online = false
		c.hub.UpdateDirectState(
			false,
			c.baseURL,
			protocol.Summary{Mode: "CDP"},
			nil,
			fmt.Sprintf("🔴 CDP unreachable at %s (%v)", c.baseURL, err),
		)
		return
	}
	c.failures = 0
	ver := c.cachedVersion(ctx)

	var pageTargets []cdpTarget
	seen := make(map[string]bool)
	for _, tgt := range targets {
		if tgt.Type == "page" {
			pageTargets = append(pageTargets, tgt)
			seen[tgt.ID] = true
		}
	}

	// Drop sessions of closed tabs.
	c.mu.Lock()
	for id, sess := range c.sessions {
		if !seen[id] {
			sess.close()
			delete(c.sessions, id)
			delete(c.tabIDs, id)
		}
	}
	c.mu.Unlock()

	// Sampling gets a hard time budget. Pages that do not answer in time keep
	// showing their last known metrics instead of stalling the whole cycle.
	cycleCtx, cancel := context.WithTimeout(ctx, c.cycleBudget)
	defer cancel()

	tabs := make([]protocol.TabInfo, len(pageTargets))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for idx, tgt := range pageTargets {
		numericID := c.numericIDFor(tgt.ID)
		sess := c.getOrCreateSession(tgt.ID, tgt.WebSocketDebuggerURL)

		wg.Add(1)
		go func(i, id int, target cdpTarget, s *session) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				s.sample(cycleCtx, c.sampleTimeout)
				<-sem
			case <-cycleCtx.Done():
				// Out of budget: fall through and report cached metrics.
			}

			tabs[i] = buildTab(id, target, s.m, i == 0)
		}(idx, numericID, tgt, sess)
	}
	wg.Wait()

	// Keep a deterministic base order by numeric tab ID so ties never shuffle rows.
	sort.Slice(tabs, func(i, j int) bool {
		return tabs[i].ID < tabs[j].ID
	})

	eventLog := ""
	if !c.online {
		c.online = true
		eventLog = fmt.Sprintf("🟢 Connected to %s", fallbackStr(ver.Browser, c.baseURL))
	}

	// Single atomic state update per cycle (eliminates visual flickering).
	c.hub.UpdateDirectState(true, c.baseURL, c.buildSummary(ver, tabs), tabs, eventLog)
}

func buildTab(id int, target cdpTarget, m metrics, first bool) protocol.TabInfo {
	estMB := 35.0 + m.heapUsedMB
	if m.heapUsedMB == 0 {
		estMB = 48.0
	}

	return protocol.TabInfo{
		ID:             id,
		TargetID:       target.ID,
		WindowID:       1,
		Index:          id, // Stable index prevents row jumping across polls
		Title:          fallbackTitle(target.Title),
		URL:            target.URL,
		Status:         "complete",
		Active:         first,
		CPUPercent:     round1(m.cpuPercent),
		NetBytesPerSec: round1(m.netRate),
		NetTotalBytes:  m.netTotal,
		JSHeapUsedMB:   round2(m.heapUsedMB),
		JSHeapTotalMB:  round2(m.heapTotalMB),
		DOMNodes:       m.domNodes,
		EstimatedMB:    round1(estMB),
	}
}

func (c *Client) getOrCreateSession(targetID, wsURL string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[targetID]; ok {
		if wsURL != "" && s.url() != wsURL {
			s.close()
			s.setURL(wsURL)
		}
		return s
	}
	s := newSession(targetID, wsURL)
	c.sessions[targetID] = s
	return s
}

func (c *Client) lookupSession(targetID string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[targetID]
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

// cachedVersion returns /json/version data, refreshing it only occasionally.
func (c *Client) cachedVersion(ctx context.Context) cdpVersion {
	if c.version.Browser != "" && time.Since(c.versionAt) < versionTTL {
		return c.version
	}
	if v, err := c.fetchVersion(ctx); err == nil {
		c.version = v
		c.versionAt = time.Now()
	}
	return c.version
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
//
// It may block on network I/O for up to a couple of seconds, so callers must
// not invoke it from a UI event loop. The TUI runs it inside a tea.Cmd. It never
// polls synchronously: instead it asks the Run loop for a refresh via kick.
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
		if err := c.putEndpoint(ctx, "/json/new?"+url.QueryEscape(targetURL)); err != nil {
			return err
		}
		c.hub.SetStatusBanner("✅ CDP opened new tab")

	case "RELOAD":
		if targetID == "" {
			return fmt.Errorf("missing targetId for RELOAD")
		}
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
		if err := c.putEndpoint(ctx, "/json/new?"+url.QueryEscape(pageURL)); err != nil {
			return err
		}
		c.hub.SetStatusBanner(fmt.Sprintf("✅ CDP duplicated target %s", shortID(targetID)))

	case "SYNC":
		c.kick()
		c.hub.SetStatusBanner("✅ CDP telemetry sync requested")
		return nil

	default:
		return fmt.Errorf("action %s is supported in Extension mode (CDP supports FOCUS, RELOAD, DUPLICATE, NEW_TAB, CLOSE, SYNC)", cmd.Action)
	}

	c.kick()
	return nil
}

func (c *Client) hitEndpoint(ctx context.Context, path string) error {
	return c.doEndpoint(ctx, http.MethodGet, path)
}

func (c *Client) putEndpoint(ctx context.Context, path string) error {
	return c.doEndpoint(ctx, http.MethodPut, path)
}

func (c *Client) doEndpoint(ctx context.Context, method, path string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
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

// sendPageCommand runs a CDP method on a page, reusing its persistent session
// when one exists.
func (c *Client) sendPageCommand(ctx context.Context, targetID, method string, params map[string]any) error {
	s := c.lookupSession(targetID)
	if s == nil {
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
		s = c.getOrCreateSession(targetID, wsURL)
	}

	_, err := s.call(ctx, method, params)
	return err
}

func fallbackTitle(t string) string {
	if strings.TrimSpace(t) == "" {
		return "(Untitled)"
	}
	return t
}

func fallbackStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
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
