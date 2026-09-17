package main

import (
	"context"
	"errors"
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/router"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

	// 创建 Listener
	listener, err := router.CreateListener(cfg)
	if err != nil {
		log.Fatalf("create listener: %v", err)
	}

	// ------------------------------------------------------------
	// Listener 和 Unix Socket 生命周期
	// ------------------------------------------------------------
	defer func() {
		if err := listener.Close(); err != nil {
			log.Printf("close listener: %v", err)
		}

		if cfg.Socket != "" {
			if err := os.Remove(cfg.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("remove unix socket %s: %v", cfg.Socket, err)
			}
		}
	}()

	// ------------------------------------------------------------
	// 监听退出信号
	// ------------------------------------------------------------
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// ------------------------------------------------------------
	// 启动 HTTP Server
	// ------------------------------------------------------------
	errCh := make(chan error, 1)

	go func() {
		if cfg.Socket != "" {
			log.Printf("kclient listening on unix socket: %s", cfg.Socket)

			errCh <- server.Serve(listener)
			return
		}

		log.Printf("kclient HTTPS listening on :%d", cfg.VNC.Port)
		errCh <- server.ServeTLS(listener, cfg.SSL.CertFile, cfg.SSL.KeyFile)
	}()

	// ------------------------------------------------------------
	// 等待退出信号或 Server 退出
	// ------------------------------------------------------------
	var serveErr error

	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")

	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
			log.Printf("server stopped unexpectedly: %v", err)
		}
	}

	// 优雅关闭
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		if serveErr != nil {
			log.Fatalf("server error: %v; shutdown error: %v", serveErr, err)
		}

		log.Fatalf("server shutdown error: %v", err)
	}

	// ------------------------------------------------------------
	// Server 非预期退出
	// ------------------------------------------------------------
	if serveErr != nil {
		log.Fatalf("server error: %v", serveErr)
	}

	log.Println("kclient stopped")
}
