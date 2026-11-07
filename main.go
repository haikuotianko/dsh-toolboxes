// Command toolbox is a portable MCP server that installs and runs common CLI
// tools (ffmpeg, rg, fd, jq, yt-dlp) and archives reusable scripts.
//
// Modes:
//
//	toolbox -mcp            MCP over stdio (a client such as DSH / opencode / Claude Code spawns it)
//	toolbox -http 127.0.0.1:8770   MCP over streamable HTTP plus the JSON API used by GUIs
//	toolbox                 prints usage
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/haikuotianko/dsh-toolboxes/internal/httpapi"
	"github.com/haikuotianko/dsh-toolboxes/internal/mcpserver"
)

// version is stamped at build time so the binary and the release tag never drift:
//
//	go build -ldflags "-X main.version=1.2.3" .
var version = "dev"

func main() {
	stdioMode := flag.Bool("mcp", false, "以 MCP stdio 模式运行（供 MCP 客户端直接拉起）")
	httpAddr := flag.String("http", "", "启动 MCP(streamable HTTP) + JSON API，例如 127.0.0.1:8770")
	rootFlag := flag.String("root", "", "工具箱目录；默认 $TOOLBOX_ROOT 或 ~/.toolbox")
	uiMode := flag.Bool("ui", false, "启动内嵌管理界面并在浏览器打开")
	setupMode := flag.Bool("setup", false, "按本机实际路径生成 DSH 用的 cordis.patch.yml（配合 -patch）")
	patchPath := flag.String("patch", "", "配合 -setup：要写入的 cordis.patch.yml 路径")
	showVersion := flag.Bool("version", false, "打印版本后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("toolbox %s\n", version)
		return
	}

	if *setupMode {
		if *patchPath == "" {
			log.Fatalf("toolbox: -setup 需要 -patch <cordis.patch.yml 路径>")
		}
		self, err := os.Executable()
		if err != nil {
			log.Fatalf("toolbox: %v", err)
		}
		target, err := resolveRoot(*rootFlag)
		if err != nil {
			log.Fatalf("toolbox: %v", err)
		}
		if err := writeDSHPatch(*patchPath, self, target); err != nil {
			log.Fatalf("toolbox: %v", err)
		}
		fmt.Printf("已写入 %s\n  command: %s\n  root:    %s\n", *patchPath, self, target)
		return
	}

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		log.Fatalf("toolbox: %v", err)
	}
	service := mcpserver.New(root, func(format string, args ...any) {
		log.Printf(format, args...)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case *stdioMode:
		log.SetOutput(os.Stderr)
		log.Printf("toolbox %s: MCP stdio, root=%s", version, root)
		if err := service.NewServer().Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Fatalf("toolbox: %v", err)
		}
	case *uiMode:
		runUI(ctx, service, root)
	case *httpAddr != "":
		runHTTP(ctx, service, *httpAddr)
	default:
		fmt.Fprintln(os.Stderr, "用法：")
		fmt.Fprintln(os.Stderr, "  toolbox -mcp                          MCP stdio（MCP 客户端拉起）")
		fmt.Fprintln(os.Stderr, "  toolbox -ui                           内嵌管理界面（浏览器打开）")
		fmt.Fprintln(os.Stderr, "  toolbox -http 127.0.0.1:8770          MCP + JSON API（无界面）")
		fmt.Fprintln(os.Stderr, "  toolbox -root D:\\tools -http :8770    指定工具箱目录")
		fmt.Fprintln(os.Stderr, "  toolbox -setup -patch <cordis.patch.yml> [-root D:\\tools]")
		fmt.Fprintln(os.Stderr, "                                        按本机路径生成 DSH 接入配置")
		fmt.Fprintln(os.Stderr, "  toolbox -version")
		os.Exit(2)
	}
}

func runHTTP(ctx context.Context, service *mcpserver.Service, addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("toolbox: listen %s: %v", addr, err)
	}
	server := &http.Server{
		Handler:           httpapi.New(service).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("toolbox %s: http://%s  (MCP: /mcp, API: /api/state)", version, listener.Addr().String())
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("toolbox: %v", err)
	}
}

// resolveRoot picks the toolbox directory: flag > $TOOLBOX_ROOT > ~/.toolbox.
func resolveRoot(flagValue string) (string, error) {
	candidate := flagValue
	if candidate == "" {
		candidate = os.Getenv("TOOLBOX_ROOT")
	}
	if candidate == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(home, ".toolbox")
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "tools"), 0o755); err != nil {
		return "", err
	}
	return absolute, nil
}
