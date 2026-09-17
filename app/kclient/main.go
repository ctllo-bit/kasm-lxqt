package main

import (
	"context"
	"errors"
	"fmt"
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/router"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const REDESKHome = "/home/remote-desktop"

func main() {
	// 加载配置
	cfg := config.Load(REDESKHome)

	// 加载认证（从 KasmVNC 的密码文件读取用户名 + SHA-256 crypt 哈希）
	vncAuth, err := auth.Load(filepath.Join(REDESKHome, ".kasmpasswd"))
	if err != nil {
		log.Fatalf("load auth: %v", err)
	}

	// 创建 HTTP Handler
	handler := router.NewHandler(cfg, vncAuth)

	// 配置未启动的 HTTP Server，设置 ReadHeaderTimeout 和 IdleTimeout
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listener, err := createListener(cfg)
	if err != nil {
		log.Fatalf("create listener: %v", err)
	}
	defer listener.Close()

	//  监听退出信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动 HTTP Server
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer(server, listener, cfg)
	}()

	// 等待退出信号或 Server 出错
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")

	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}

		return
	}

	// 优雅关闭
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Println("kclient stopped")
}

func httpServer(server *http.Server, listener net.Listener, cfg config.Config) error {
	if cfg.Socket != "" {
		log.Printf("kclient listening on unix socket: %s", cfg.Socket)
		return server.Serve(listener)
	}

	log.Printf("kclient HTTPS listening on :%d", cfg.VNC.Port)
	return server.ServeTLS(listener, cfg.SSL.CertFile, cfg.SSL.KeyFile)
}

func createListener(cfg config.Config) (net.Listener, error) {
	// ------------------------------------------------------------
	// Unix Socket
	// ------------------------------------------------------------
	if cfg.Socket != "" {
		socketPath := cfg.Socket

		// 删除旧 Socket
		if err := os.Remove(socketPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove old unix socket %s: %w", socketPath, err)
		}

		// 创建 Unix Socket
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("listen unix socket %s: %w", socketPath, err)
		}

		// 设置 Socket 权限
		if err := os.Chmod(socketPath, 0660); err != nil {
			_ = listener.Close()
			_ = os.Remove(socketPath)

			return nil, fmt.Errorf("chmod unix socket %s: %w", socketPath, err)
		}

		return listener, nil
	}

	// ------------------------------------------------------------
	// TCP + HTTPS
	// ------------------------------------------------------------
	addr := ":" + strconv.Itoa(cfg.VNC.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen tcp %s: %w", addr, err)
	}

	return listener, nil
}
