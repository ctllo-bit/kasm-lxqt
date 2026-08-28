package main

import (
	"context"
	"crypto/tls"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type pageData struct {
	Title string
	Path  string
}

func main() {
	cfg := loadConfig()
	baseDir := resolveBaseDir()
	handler := newServer(cfg, baseDir)

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var listener net.Listener
	var err error

	// 智能判断启动模式：Unix Socket 或 TCP 端口
	if cfg.Socket != "" {
		// 清理残留的旧 Socket 文件
		if err := os.Remove(cfg.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Fatalf("remove old socket: %v", err)
		}

		listener, err = net.Listen("unix", cfg.Socket)
		if err != nil {
			log.Fatalf("listen unix socket %s: %v", cfg.Socket, err)
		}
		defer os.Remove(cfg.Socket)

		// 给 nginx/fnOS 等网关访问权限
		if err := os.Chmod(cfg.Socket, 0660); err != nil {
			log.Printf("chmod socket warning: %v", err)
		}
		log.Printf("kclient listening on unix socket: %s", cfg.Socket)

	} else {
		addr := ":" + strconv.Itoa(cfg.VNC.Port)
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("listen tcp %s: %v", addr, err)
		}
		log.Printf("kclient listening on port %d (subfolder %q)", cfg.VNC.Port, cfg.Subfolder)
	}

	// ------------------------------------------------------------
	// 优雅退出 (Graceful Shutdown)
	// ------------------------------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server run error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutdown signal received, shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

func newServer(cfg Config, baseDir string) http.Handler {
	publicDir := filepath.Join(baseDir, "public")
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "index.html")))
	manifestTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "manifest.json")))

	files := &filesHub{
		root:          cleanRoot(cfg.FMHome),
		maxUploadSize: cfg.MaxUploadSize,
	}

	audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)

	// ------------------------------------------------------------
	// 构建 KasmVNC 反向代理 (合并原本的 vncproxy 逻辑)
	// ------------------------------------------------------------
	targetURL, err := url.Parse(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("invalid VNC proxy target %q: %v", cfg.VNC.ProxyTarget, err)
	}

	vncProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 保留并克隆原有的 Header，支持 WebSocket 和认证透传
			pr.Out.Header = pr.In.Header.Clone()

			// 重写目标 URL
			pr.SetURL(targetURL)

			// KasmVNC 强依赖上游 Host 一致性
			pr.Out.Host = targetURL.Host

			// 避免 RawPath 导致反向代理寻找错误资源
			pr.Out.URL.RawPath = ""

			log.Printf("PROXY OUT: method=%s path=%s host=%s upgrade=%q",
				pr.In.Method, pr.Out.URL.Path, pr.Out.Host, pr.In.Header.Get("Upgrade"))
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 仅用于本机 KasmVNC 自签名证书
			},
			ForceAttemptHTTP2: false, // 关闭有助于稳定代理 WebSocket
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 400 {
				log.Printf("KasmVNC ERROR RESPONSE: status=%d path=%s", resp.StatusCode, resp.Request.URL.Path)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("KasmVNC PROXY ERROR: method=%s path=%s err=%v", r.Method, r.URL.Path, err)
			http.Error(w, "KasmVNC upstream unavailable", http.StatusBadGateway)
		},
	}

	// ------------------------------------------------------------
	// 路由注册
	// ------------------------------------------------------------
	mux := http.NewServeMux()

	// 首页与静态文件
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, indexTmpl, pageData{Title: cfg.Title, Path: cfg.VNCPath()}, "text/html; charset=utf-8")
	})

	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, manifestTmpl, pageData{Title: cfg.Title}, "application/json; charset=utf-8")
	})

	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(publicDir, "favicon.ico"))
	})

	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(publicDir, "filebrowser.html"))
	})

	mux.HandleFunc("GET /files/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(publicDir, "filebrowser.html"))
	})

	mux.Handle("/public/", http.StripPrefix("/public", http.FileServer(http.Dir(publicDir))))

	// KasmVNC 反向代理路由 (HTTP 与 WebSocket 合并处理)
	// /vnc/* 自动剥离前缀发往代理
	mux.Handle("/vnc/", http.StripPrefix("/vnc", vncProxy))

	// WebSocket 原样发往代理
	mux.Handle("/websockify", vncProxy)
	mux.Handle("/websockify/", vncProxy)

	// 本地 WebSocket 路由
	mux.HandleFunc("GET /files/ws", files.handleWS)
	mux.HandleFunc("GET /audio/ws", audio.handleWS)

	// 健康检查
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 调试捕获
	mux.HandleFunc("/__debug_unmatched__", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UNMATCHED DEBUG PATH=%s Host=%s Remote=%s", r.URL.Path, r.Host, r.RemoteAddr)
		http.NotFound(w, r)
	})

	return mount(cfg.Subfolder, mux)
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data pageData, contentType string) {
	w.Header().Set("Content-Type", contentType)
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func mount(subfolder string, handler http.Handler) http.Handler {
	if subfolder == "/" || subfolder == "" {
		return handler
	}
	prefix := strings.TrimSuffix(subfolder, "/")
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
	})
	return mux
}

func cleanRoot(root string) string {
	if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

func resolveBaseDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "public")); err == nil {
			return dir
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "public")); err == nil {
			return cwd
		}
	}
	return "."
}
