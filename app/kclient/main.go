package main

import (
	"errors"
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/listener"
	"kclient/internal/proxy"
	"kclient/internal/router"
	"log"
	"net/http"
	"path/filepath"
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

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 全部 HTTPS：统一走 ServeTLS
	if err := server.ServeTLS(ln, cfg.SSL.CertFile, cfg.SSL.KeyFile); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}

}
