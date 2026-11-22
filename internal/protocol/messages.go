package protocol

import "time"

// TabInfo represents real-time tab/target state, CPU, network, JS heap, and memory footprint.
type TabInfo struct {
	ID              int     `json:"id"`
	TargetID        string  `json:"targetId,omitempty"`
	WindowID        int     `json:"windowId"`
	Index           int     `json:"index"`
	ProcessID       int     `json:"processId,omitempty"`
	Title           string  `json:"title"`
	URL             string  `json:"url"`
	Status          string  `json:"status"`
	Active          bool    `json:"active"`
	Pinned          bool    `json:"pinned"`
	Audible         bool    `json:"audible"`
	Muted           bool    `json:"muted"`
	Discarded       bool    `json:"discarded"`
	AutoDiscardable bool    `json:"autoDiscardable"`
	CPUPercent      float64 `json:"cpuPercent"`
	NetBytesPerSec  float64 `json:"netBytesPerSec"`
	NetTotalBytes   uint64  `json:"netTotalBytes"`
	JSHeapUsedMB    float64 `json:"jsHeapUsedMb"`
	JSHeapTotalMB   float64 `json:"jsHeapTotalMb"`
	DOMNodes        int     `json:"domNodes"`
	EstimatedMB     float64 `json:"estimatedMb"`
}

// Summary holds aggregated browser metrics across all open windows and tabs.
type Summary struct {
	Mode           string  `json:"mode"` // "EXTENSION" | "CDP"
	TotalTabs      int     `json:"totalTabs"`
	TotalWindows   int     `json:"totalWindows"`
	ActiveTabs     int     `json:"activeTabs"`
	AudibleTabs    int     `json:"audibleTabs"`
	MutedTabs      int     `json:"mutedTabs"`
	DiscardedTabs  int     `json:"discardedTabs"`
	PinnedTabs     int     `json:"pinnedTabs"`
	TotalCPUPerc   float64 `json:"totalCpuPercent"`
	TotalNetRate   float64 `json:"totalNetBytesPerSec"`
	TotalJSHeapMB  float64 `json:"totalJsHeapMb"`
	EstimatedTotal float64 `json:"estimatedTotalMb"`
	UserAgent      string  `json:"userAgent"`
	Platform       string  `json:"platform"`
}

// TelemetryMsg is pushed periodically or event-driven from the Chrome extension to Go.
type TelemetryMsg struct {
	Type    string    `json:"type"` // "TELEMETRY"
	SentAt  int64     `json:"sentAt"`
	Summary Summary   `json:"summary"`
	Tabs    []TabInfo `json:"tabs"`
}

// CommandMsg is dispatched from the Go TUI to the Chrome extension or CDP controller.
type CommandMsg struct {
	Type      string `json:"type"`   // "COMMAND"
	Action    string `json:"action"` // "FOCUS" | "CLOSE" | "RELOAD" | "DISCARD" | "MUTE" | "PIN" | "DUPLICATE" | "NEW_TAB" | "SYNC"
	TabID     int    `json:"tabId,omitempty"`
	TargetID  string `json:"targetId,omitempty"`
	WindowID  int    `json:"windowId,omitempty"`
	URL       string `json:"url,omitempty"`
	RequestID string `json:"requestId"`
}

// AckMsg is sent by the Chrome extension after executing a CommandMsg.
type AckMsg struct {
	Type      string `json:"type"` // "ACK"
	RequestID string `json:"requestId"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
}

// Snapshot provides a safe, immutable view of the current state for the TUI.
type Snapshot struct {
	// Revision increases on every state change. Consumers can use it to skip
	// redundant work when nothing has changed since the previous snapshot.
	Revision     uint64
	Mode         string
	Connected    bool
	PeerAddr     string
	LastSync     time.Time
	Summary      Summary
	Tabs         []TabInfo
	LastEventLog string
}
