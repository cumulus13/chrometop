package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cumulus13/chrometop/internal/cdp"
	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/ui"
)

// Build metadata, injected with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "none"
)

func main() {
	bindAddr := flag.String("addr", "127.0.0.1:9333", "Local WebSocket listen address for Chrome Extension mode")
	token := flag.String("token", "", "Optional shared token for Chrome Extension authentication")
	cdpURL := flag.String("cdp", "", "Optional Chromium/Chrome/Brave remote debugging URL (e.g. http://127.0.0.1:9222) for direct CDP mode")
	pollInterval := flag.Duration("interval", 1*time.Second, "Polling interval in direct CDP mode")
	newTabURL := flag.String("new-tab-url", "about:blank", "URL opened by the 'n' (new tab) key")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("chrometop %s (commit %s)\n", version, commit)
		return
	}

	if *cdpURL == "" && !isLoopbackAddr(*bindAddr) && *token == "" {
		fmt.Fprintf(os.Stderr, "warning: listening on non-loopback address %s without --token; anyone on the network can read tab data and control your browser\n", *bindAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The hub is passive: it only stores state and signals subscribers. The UI
	// pulls from it, so no backend goroutine can ever block on the UI.
	wsHub := hub.New(*token, nil)

	endpointLabel := "ws://" + *bindAddr + "/ws"
	if *cdpURL != "" {
		endpointLabel = *cdpURL
	}

	model := ui.NewModel(wsHub, endpointLabel).WithNewTabURL(*newTabURL)
	app := tea.NewProgram(model, tea.WithAltScreen())

	serverErr := make(chan error, 1)

	if *cdpURL != "" {
		cdpClient := cdp.NewClient(*cdpURL, wsHub)
		wsHub.SetDispatcher("CDP", cdpClient)
		go cdpClient.Run(ctx, *pollInterval)
	} else {
		go func() {
			if err := hub.ListenAndServe(ctx, *bindAddr, wsHub); err != nil {
				serverErr <- err
				app.Quit()
			}
		}()
	}

	go func() {
		<-ctx.Done()
		app.Quit()
	}()

	_, runErr := app.Run()
	stop()

	select {
	case err := <-serverErr:
		fmt.Fprintf(os.Stderr, "WebSocket server error: %v\n", err)
		os.Exit(1)
	default:
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "TUI runtime error: %v\n", runErr)
		os.Exit(1)
	}
}

// isLoopbackAddr reports whether addr binds only to the loopback interface.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
