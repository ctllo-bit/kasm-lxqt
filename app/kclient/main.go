package main

import (
	"context"
	"errors"
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

	//  监听退出信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动 HTTP Server
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer(server, cfg)
	}()

	// 收到 SIGTERM / SIGINT 或 HTTP Server 出错
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")

	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}

		return
	}

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	// 优雅关闭
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Println("kclient stopped")
}

func httpServer(server *http.Server, cfg config.Config) error {
	if cfg.Socket != "" {
		socketPath := cfg.Socket

		// 启动前清理旧 Socket
		if err := os.Remove(socketPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}

		// 创建 Unix Socket
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return err
		}

		// Serve 返回后关闭 Listener
		defer listener.Close()

		// Server 结束后删除 Socket 文件
		defer os.Remove(socketPath)

		if err := os.Chmod(socketPath, 0660); err != nil {
			return err
		}

		log.Printf("kclient listening on unix socket: %s", socketPath)
		// 启动 HTTP Server
		return server.Serve(listener)
	}

	// 创建 TCP Listener
	addr := ":" + strconv.Itoa(cfg.VNC.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	// 检查 HTTPS 配置
	if cfg.SSL.CertFile == "" {
		return errors.New("HTTPS certificate file is empty")
	}

	if cfg.SSL.KeyFile == "" {
		return errors.New("HTTPS private key file is empty")
	}

	log.Printf("kclient HTTPS listening on :%d", cfg.VNC.Port)
	// 启动 HTTPS Server
	return server.ServeTLS(listener, cfg.SSL.CertFile, cfg.SSL.KeyFile)
}
