package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cumulus13/chrometop/internal/protocol"
	"github.com/gorilla/websocket"
)

const (
	writeDeadline = 5 * time.Second
	pongDeadline  = 15 * time.Second
	pingInterval  = 5 * time.Second
	maxFrameBytes = 4 * 1024 * 1024
)

// Server manages the WebSocket connection from the Chrome extension and stores live telemetry.
type Server struct {
	mu           sync.RWMutex
	writeMu      sync.Mutex
	conn         *websocket.Conn
	connected    bool
	peerAddr     string
	lastSync     time.Time
	summary      protocol.Summary
	tabs         []protocol.TabInfo
	lastEventLog string
	token        string
	upgrader     websocket.Upgrader
	onChange     func()
}

// New constructs a new WebSocket hub.
func New(token string, onChange func()) *Server {
	return &Server{
		token:    token,
		onChange: onChange,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  16384,
			WriteBufferSize: 16384,
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimSpace(r.Header.Get("Origin"))
				return origin == "" || strings.HasPrefix(origin, "chrome-extension://")
			},
		},
	}
}

// HandleWS upgrades HTTP requests from the Chrome extension to a WebSocket session.
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	if s.token != "" {
		if r.URL.Query().Get("token") != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.attach(conn, r.RemoteAddr)
	defer s.detach(conn)

	conn.SetReadLimit(maxFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(pongDeadline))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongDeadline))
		return nil
	})

	stopPing := make(chan struct{})
	go s.heartbeat(conn, stopPing)
	defer close(stopPing)

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			break
		}
		s.consumeFrame(payload)
	}
}

func (s *Server) attach(conn *websocket.Conn, peer string) {
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn = conn
	s.connected = true
	s.peerAddr = peer
	s.lastEventLog = fmt.Sprintf("🟢 Extension connected from %s", peer)
	s.mu.Unlock()
	s.emitChange()
}

func (s *Server) detach(conn *websocket.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
		s.connected = false
		s.peerAddr = ""
		s.lastEventLog = "🔴 Extension disconnected"
	}
	s.mu.Unlock()
	_ = conn.Close()
	s.emitChange()
}

func (s *Server) heartbeat(conn *websocket.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			s.writeMu.Unlock()
			if err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func (s *Server) consumeFrame(raw []byte) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return
	}

	switch header.Type {
	case "TELEMETRY":
		var msg protocol.TelemetryMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.summary = msg.Summary
		s.tabs = msg.Tabs
		s.lastSync = time.Now()
		s.mu.Unlock()
		s.emitChange()

	case "ACK":
		var ack protocol.AckMsg
		if err := json.Unmarshal(raw, &ack); err != nil {
			return
		}
		s.mu.Lock()
		if ack.OK {
			s.lastEventLog = fmt.Sprintf("✅ %s", ack.Detail)
		} else {
			s.lastEventLog = fmt.Sprintf("❌ %s", ack.Detail)
		}
		s.mu.Unlock()
		s.emitChange()
	}
}

// SendCommand transmits a control command to the connected Chrome extension.
func (s *Server) SendCommand(cmd protocol.CommandMsg) error {
	s.mu.RLock()
	conn := s.conn
	ok := s.connected
	s.mu.RUnlock()

	if !ok || conn == nil {
		return errors.New("no active Chrome extension WebSocket connection")
	}

	encoded, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))
	return conn.WriteMessage(websocket.TextMessage, encoded)
}

// SetStatusBanner updates the event log line directly from the TUI.
func (s *Server) SetStatusBanner(msg string) {
	s.mu.Lock()
	s.lastEventLog = msg
	s.mu.Unlock()
	s.emitChange()
}

// Snapshot returns a safe copy of current state for the TUI.
func (s *Server) Snapshot() protocol.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tabs := make([]protocol.TabInfo, len(s.tabs))
	copy(tabs, s.tabs)

	return protocol.Snapshot{
		Connected:    s.connected,
		PeerAddr:     s.peerAddr,
		LastSync:     s.lastSync,
		Summary:      s.summary,
		Tabs:         tabs,
		LastEventLog: s.lastEventLog,
	}
}

func (s *Server) emitChange() {
	if s.onChange != nil {
		s.onChange()
	}
}

// ListenAndServe binds the HTTP/WebSocket server and gracefully shuts down on context cancellation.
func ListenAndServe(ctx context.Context, addr string, srvHub *Server) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srvHub.HandleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
