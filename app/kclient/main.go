package main

import (
	"context"
	"errors"
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/listener"
	"kclient/internal/proxy"
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
	// 1. 加载配置
	cfg := config.Load(REDESKHome)

	// 2. 加载认证（从 KasmVNC 的密码文件读取用户名 + SHA-256 crypt 哈希）
	a, err := auth.Load(filepath.Join(REDESKHome, ".kasmpasswd"))
	if err != nil {
		log.Fatalf("load auth: %v", err)
	}

	// 3. 创建 KasmVNC 反向代理
	vncProxy, err := proxy.New(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("create vnc proxy: %v", err)
	}

	// 4. 组装路由（模板解析、路由注册、认证中间件都在这里）
	handler, err := router.New(cfg, a, vncProxy)
	if err != nil {
		log.Fatalf("build router: %v", err)
	}

	// // 5. 启动 HTTPS 服务器
	// addr := fmt.Sprintf(":%d", cfg.VNC.Port)
	// log.Printf("listening on https://%s", addr)
	// log.Fatal(http.ListenAndServeTLS(addr, cfg.SSL.CertFile, cfg.SSL.KeyFile, handler))

	opt := listener.Options{
		Socket: cfg.Socket,
		Port:   cfg.VNC.Port,
	}

	ln, err := listener.New(opt)
	if err != nil {
		log.Fatalf("create listener: %v", err)
	}
	defer ln.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		log.Println("server started")
		var err error
		if cfg.Socket != "" {
			err = server.Serve(ln)
		} else {
			err = server.ServeTLS(
				ln,
				cfg.SSL.CertFile,
				cfg.SSL.KeyFile,
			)
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Println("shutdown signal")
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server error:%v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf(
			"server shutdown error: %v",
			err,
		)
	}

	if cfg.Socket != "" {

		if err := os.Remove(cfg.Socket); err != nil &&
			!errors.Is(err, os.ErrNotExist) {

			log.Printf(
				"remove socket %s: %v",
				cfg.Socket,
				err,
			)
		}
	}

}
