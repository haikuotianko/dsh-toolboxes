package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/haikuotianko/toolbox/internal/httpapi"
	"github.com/haikuotianko/toolbox/internal/mcpserver"
)

//go:embed web/index.html
var uiFiles embed.FS

func uiHTML() []byte {
	data, err := uiFiles.ReadFile("web/index.html")
	if err != nil {
		return []byte("<h1>toolbox</h1><p>embedded page missing</p>")
	}
	return data
}

// runUI serves the embedded manager page on a random loopback port and opens the
// system browser at it. No cgo/webview dependency: this works on both Windows
// and macOS with a plain Go build.
func runUI(ctx context.Context, service *mcpserver.Service, root string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("toolbox: listen: %v", err)
	}
	api := httpapi.New(service)
	api.UI = uiHTML()
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}

	url := fmt.Sprintf("http://%s/", listener.Addr().String())
	log.Printf("toolbox ui: %s  (root %s)", url, root)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	openBrowser(url)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("toolbox: %v", err)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("toolbox: 打不开浏览器，请手动访问 %s (%v)", url, err)
	}
}
