package main

import (
	"kclient/config"
	"kclient/internal/auth"
	"kclient/internal/proxy"
	"kclient/internal/router"
	"log"
	"net/http"
	"path/filepath"
)

const REDESKHome = "/home/remote-desktop"

func main() {
	// 加载配置
	cfg := config.Load(REDESKHome)

	// 创建 KasmVNC 反向代理
	vncProxy, err := proxy.New(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("create vnc proxy: %v", err)
	}

	// 加载认证（从 KasmVNC 的密码文件读取用户名 + SHA-256 crypt 哈希）
	vncAuth, err := auth.Load(filepath.Join(REDESKHome, ".kasmpasswd"))
	if err != nil {
		log.Fatalf("load auth: %v", err)
	}

	// 3. 创建 HTTP Handler
	handler, err := router.NewHandler(cfg, vncAuth, vncProxy)
	if err != nil {
		log.Fatalf("build router: %v", err)
	}

	// 4. 创建监听器
	ln, err := router.New(router.Options{
		Socket: cfg.Socket,
		Port:   cfg.VNC.Port,
	})

	if err != nil {
		log.Fatal(err)
	}

	// 5. 启动 HTTP Server

	server := http.Server{
		Handler: handler,
	}

	log.Println("kclient started")

	err = server.Serve(ln)

	log.Fatal(err)

}
