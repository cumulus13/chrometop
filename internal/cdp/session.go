package cdp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// maxFrameBytes bounds a single CDP frame read from a page WebSocket.
	maxFrameBytes = 8 * 1024 * 1024

	// dialTimeout bounds the WebSocket handshake with a page target.
	dialTimeout = 400 * time.Millisecond

	// backoffAfterFailures is the number of consecutive failed samples after
	// which a session is closed and left alone for a growing back-off period.
	backoffAfterFailures = 3

	// maxBackoffSeconds caps the back-off period for unresponsive pages.
	maxBackoffSeconds = 10
)

var (
	errClosed = errors.New("cdp session closed")

	// CDP events start with {"method": ...}; responses start with {"id": ...}.
	// Checking the prefix lets the reader skip decoding of high-volume events.
	eventPrefix = []byte(`{"method"`)
)

// rpcError is the error object of a CDP response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// response is a decoded CDP response frame.
type response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// metrics holds the last known measurements of a page so that the UI never
// flickers to zero when a single sample fails or times out.
type metrics struct {
	cpuPercent  float64
	netRate     float64
	netTotal    uint64
	heapUsedMB  float64
	heapTotalMB float64
	domNodes    int
}

// session is a persistent CDP connection to a single page target.
//
// A dedicated reader goroutine owns all reads, which means a slow or busy page
// can never leave the socket in the corrupted state that gorilla/websocket
// enters after a read deadline expires. Requests are correlated with their
// responses through unique ids, so late responses from earlier samples are
// simply dropped.
type session struct {
	targetID string
	wsURL    string

	dialMu  sync.Mutex // serialises dialing
	writeMu sync.Mutex // serialises socket writes

	mu      sync.Mutex // guards wsURL and the fields below
	conn    *websocket.Conn
	enabled bool
	nextReq int64
	pending map[int64]chan response

	// Sampling state. Only touched by the goroutine currently sampling.
	lastCPU      float64
	lastNet      uint64
	lastSampleAt time.Time
	failures     int
	backoffUntil time.Time
	m            metrics
}

func newSession(targetID, wsURL string) *session {
	return &session{targetID: targetID, wsURL: wsURL}
}

func (s *session) url() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wsURL
}

func (s *session) setURL(u string) {
	s.mu.Lock()
	s.wsURL = u
	s.mu.Unlock()
}

// ensureConn returns the live connection, dialing a new one when needed.
func (s *session) ensureConn(ctx context.Context) (*websocket.Conn, error) {
	s.dialMu.Lock()
	defer s.dialMu.Unlock()

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	wsURL := s.url()
	if wsURL == "" {
		return nil, errors.New("target has no debugger URL")
	}

	dialer := websocket.Dialer{HandshakeTimeout: dialTimeout}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxFrameBytes)

	s.mu.Lock()
	s.conn = conn
	s.enabled = false
	s.pending = make(map[int64]chan response)
	s.mu.Unlock()

	go s.readLoop(conn)
	return conn, nil
}

// readLoop is the only reader of conn. It exits when the socket fails.
func (s *session) readLoop(conn *websocket.Conn) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			s.fail(conn)
			return
		}
		if bytes.HasPrefix(raw, eventPrefix) {
			continue
		}

		var r response
		if json.Unmarshal(raw, &r) != nil || r.ID == 0 {
			continue
		}

		s.mu.Lock()
		ch, ok := s.pending[r.ID]
		if ok {
			delete(s.pending, r.ID)
		}
		s.mu.Unlock()

		if ok {
			ch <- r // buffered (cap 1) and sent at most once per id
		}
	}
}

// fail tears down conn and releases every waiter with an error.
func (s *session) fail(conn *websocket.Conn) {
	var pend map[int64]chan response

	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
		s.enabled = false
		pend = s.pending
		s.pending = nil
	}
	s.mu.Unlock()

	_ = conn.Close()
	for _, ch := range pend {
		ch <- response{Error: &rpcError{Message: errClosed.Error()}}
	}
}

// close drops the current connection, if any.
func (s *session) close() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		s.fail(conn)
	}
}

// send writes a request without waiting for its response.
func (s *session) send(conn *websocket.Conn, method string, params map[string]any, deadline time.Time) (int64, chan response, error) {
	ch := make(chan response, 1)

	s.mu.Lock()
	if s.conn != conn || s.pending == nil {
		s.mu.Unlock()
		return 0, nil, errClosed
	}
	s.nextReq++
	id := s.nextReq
	s.pending[id] = ch
	s.mu.Unlock()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}

	s.writeMu.Lock()
	_ = conn.SetWriteDeadline(deadline)
	err := conn.WriteJSON(msg)
	s.writeMu.Unlock()
	if err != nil {
		s.forget(id)
		s.fail(conn)
		return 0, nil, err
	}
	return id, ch, nil
}

// forget removes a pending request that nobody waits for anymore.
func (s *session) forget(id int64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// await blocks until the response arrives or ctx is done.
func (s *session) await(ctx context.Context, id int64, ch chan response) (response, error) {
	select {
	case r := <-ch:
		if r.Error != nil {
			return r, fmt.Errorf("cdp error: %s", r.Error.Message)
		}
		return r, nil
	case <-ctx.Done():
		s.forget(id)
		return response{}, ctx.Err()
	}
}

// call performs one request/response round trip.
func (s *session) call(ctx context.Context, method string, params map[string]any) (response, error) {
	conn, err := s.ensureConn(ctx)
	if err != nil {
		return response{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Second)
	}
	id, ch, err := s.send(conn, method, params, deadline)
	if err != nil {
		return response{}, err
	}
	return s.await(ctx, id, ch)
}

// noteFailure records a failed sample and starts back-off for repeat offenders.
func (s *session) noteFailure() {
	s.failures++
	if s.failures < backoffAfterFailures {
		return
	}
	secs := s.failures
	if secs > maxBackoffSeconds {
		secs = maxBackoffSeconds
	}
	s.backoffUntil = time.Now().Add(time.Duration(secs) * time.Second)
	s.close()
}

func (s *session) noteSuccess() {
	s.failures = 0
	s.backoffUntil = time.Time{}
}

// sample collects one set of measurements. On any failure the cached metrics
// from the previous successful sample are kept.
func (s *session) sample(ctx context.Context, timeout time.Duration) {
	if ctx.Err() != nil || s.url() == "" || time.Now().Before(s.backoffUntil) {
		return
	}

	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := s.ensureConn(sctx)
	if err != nil {
		s.noteFailure()
		return
	}
	deadline := time.Now().Add(timeout)

	// Requests are pipelined: Chrome handles them in order per session, so there
	// is no need to wait for Performance.enable before asking for metrics.
	s.mu.Lock()
	needEnable := !s.enabled
	s.mu.Unlock()

	var enableID int64
	var enableCh chan response
	if needEnable {
		enableID, enableCh, err = s.send(conn, "Performance.enable", nil, deadline)
		if err != nil {
			s.noteFailure()
			return
		}
		s.mu.Lock()
		s.enabled = true
		s.mu.Unlock()
	}

	metricsID, metricsCh, err := s.send(conn, "Performance.getMetrics", nil, deadline)
	if err != nil {
		s.noteFailure()
		return
	}

	evalParams := map[string]any{
		"expression":    evalExpression,
		"returnByValue": true,
	}
	evalID, evalCh, err := s.send(conn, "Runtime.evaluate", evalParams, deadline)
	if err != nil {
		s.noteFailure()
		return
	}

	if needEnable {
		_, _ = s.await(sctx, enableID, enableCh)
	}
	metricsRes, metricsErr := s.await(sctx, metricsID, metricsCh)
	evalRes, evalErr := s.await(sctx, evalID, evalCh)

	if metricsErr != nil {
		s.noteFailure()
		return
	}

	var parsed struct {
		Metrics []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(metricsRes.Result, &parsed); err != nil {
		s.noteFailure()
		return
	}

	var taskDuration, threadTime float64
	for _, mt := range parsed.Metrics {
		switch mt.Name {
		case "TaskDuration":
			taskDuration = mt.Value
		case "ThreadTime":
			threadTime = mt.Value
		case "JSHeapUsedSize":
			s.m.heapUsedMB = mt.Value / (1024 * 1024)
		case "JSHeapTotalSize":
			s.m.heapTotalMB = mt.Value / (1024 * 1024)
		case "Nodes":
			s.m.domNodes = int(mt.Value)
		}
	}

	if evalErr == nil {
		var ev struct {
			Result struct {
				Value float64 `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(evalRes.Result, &ev) == nil && ev.Result.Value > 0 {
			s.m.netTotal = uint64(ev.Result.Value)
		}
	}

	cpuClock := taskDuration
	if cpuClock <= 0 && threadTime > 0 {
		cpuClock = threadTime
	}

	now := time.Now()
	if !s.lastSampleAt.IsZero() && cpuClock > 0 {
		dt := now.Sub(s.lastSampleAt).Seconds()
		if dt > 0.05 {
			if dCPU := cpuClock - s.lastCPU; dCPU >= 0 {
				pct := (dCPU / dt) * 100.0
				if pct > 100.0 {
					pct = 100.0
				}
				s.m.cpuPercent = pct
			}
			if s.m.netTotal >= s.lastNet {
				s.m.netRate = float64(s.m.netTotal-s.lastNet) / dt
			}
		}
	}

	if cpuClock > 0 {
		s.lastCPU = cpuClock
	}
	s.lastNet = s.m.netTotal
	s.lastSampleAt = now
	s.noteSuccess()
}

// evalExpression sums the transfer size of all resources loaded by the page.
const evalExpression = "(() => { try { return performance.getEntriesByType('resource').reduce((a, b) => a + (b.transferSize || 0), 0); } catch (e) { return 0; } })()"
