package main

import (
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/router"
	"log"
	"net/http"
	"path/filepath"
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

	// 启动 HTTP Server
	server.ListenAndServeTLS(cfg.SSL.CertFile, cfg.SSL.KeyFile)

}
