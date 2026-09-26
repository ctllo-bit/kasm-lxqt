package main

import (
	"context"
	"errors"
	"fmt"
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
	// 使用 run() 模式，确保内部的 defer 逻辑在退出前一定会被执行
	if err := run(); err != nil {
		log.Fatalf("kclient exited with error: %v", err)
	}
	log.Println("kclient stopped gracefully")
}

func run() error {
	// 加载配置
	cfg := config.Load(REDESKHome)

	// 加载认证（从 KasmVNC 的密码文件读取用户名 + SHA-256 crypt 哈希）
	vncAuth, err := auth.Load(filepath.Join(REDESKHome, ".kasmpasswd"))
	if err != nil {
		return fmt.Errorf("load auth: %w", err)
	}

	// 创建 Handler
	handler, err := router.NewHandler(cfg, vncAuth)
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}

	// 创建 Listener
	listener, err := router.CreateListener(cfg)
	if err != nil {
		return fmt.Errorf("create listener: %w", err)
	}

	// 配置 HTTP Server（增加 ReadTimeout / WriteTimeout 防止慢速连接攻击）
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 确保 Unix Socket 文件在退出时被清理
	if cfg.Mode == "gateway" && cfg.Listen.Socket != "" {
		defer func() {
			if err := os.Remove(cfg.Listen.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("remove unix socket %s error: %v", cfg.Listen.Socket, err)
			} else {
				log.Printf("unix socket %s cleaned up", cfg.Listen.Socket)
			}
		}()
	}

	// 监听退出信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动 HTTP Server
	errCh := make(chan error, 1)
	go func() {
		switch cfg.Mode {
		case "gateway":
			log.Printf("kclient listening on unix socket: %s", cfg.Listen.Socket)
			errCh <- server.Serve(listener)
		case "port":
			log.Printf("kclient HTTPS listening on :%d", cfg.Listen.Port)
			errCh <- server.ServeTLS(listener, cfg.SSL.CertFile, cfg.SSL.KeyFile)
		default:
			errCh <- fmt.Errorf("invalid mode: %q", cfg.Mode)
		}
	}()

	// 等待退出信号或 Server 异常退出
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received, starting graceful shutdown...")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server stopped unexpectedly: %w", err)
		}
		// 如果是正常的 http.ErrServerClosed 退出，无需报错
		return nil
	}

	// 优雅关闭 Server（最多等待 5 秒处理未完成的请求）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown error: %w", err)
	}

	return nil
}
