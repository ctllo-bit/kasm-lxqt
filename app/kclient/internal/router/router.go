package router

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"kclient/config"
	"kclient/internal/audio"
	"kclient/internal/auth"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type pageData struct {
	Title   string
	VNCPath string
}

const kclientDir = "/var/apps/kasm-lxqt/target/kclient"

func NewHandler(cfg config.Config, auth *auth.Authenticator) http.Handler {
	// 根目录资源
	publicDir := filepath.Join(kclientDir, "public")
	// 加载 index.html 模板
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "index.html")))

	//files := &filesHub{root: cleanRoot(cfg.FMHome), maxUploadSize: cfg.MaxUploadSize}
	//audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)

	// ------------------------------------------------------------
	// KasmVNC ReverseProxy
	// ------------------------------------------------------------
	vncProxy, err := newVNCProxy(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("create KasmVNC proxy: %v", err)
	}

	// ------------------------------------------------------------
	//  HTTP 请求多路复用器(路由器)，用来根据请求的 URL 路径，分发给不同的处理函数
	// ------------------------------------------------------------
	mux := http.NewServeMux()

	// ------------------------------------------------------------
	// Kclient 静态资源
	// ------------------------------------------------------------
	kclientStatic := http.FileServer(http.Dir(publicDir))
	mux.Handle("/public/", http.StripPrefix("/public/", kclientStatic))

	// manifest / favicon
	mux.HandleFunc("/manifest.json", staticFile(filepath.Join(publicDir, "manifest.json"), "application/manifest+json"))
	mux.HandleFunc("/favicon.ico", staticFile(filepath.Join(publicDir, "favicon.ico"), "image/x-icon"))

	// ------------------------------------------------------------
	// 首页
	// ------------------------------------------------------------
	mux.HandleFunc("GET /{$}",
		func(w http.ResponseWriter, r *http.Request) {
			log.Printf("render index: subfolder=%q vncPath=%q", cfg.Subfolder, cfg.VNCPath())
			renderTemplate(w, indexTmpl, pageData{Title: cfg.Title, VNCPath: cfg.VNCPath()})
		},
	)

	// ------------------------------------------------------------
	// KasmVNC
	//
	// /vnc/index.html
	//     ↓
	// https://127.0.0.1:6901/index.html
	// ------------------------------------------------------------
	mux.Handle("/vnc/", http.StripPrefix("/vnc", http.FileServer(http.Dir("/usr/share/kasmvnc/www/"))))

	// ------------------------------------------------------------
	// KasmVNC WebSocket
	//
	// /websockify
	// /websockify/*
	// ------------------------------------------------------------
	mux.Handle("/websockify", vncProxy)
	mux.Handle("/websockify/", vncProxy)

	// ------------------------------------------------------------
	// Audio WebRTC
	// ------------------------------------------------------------
	mux.HandleFunc("POST /audio/offer", audio.HandleOffer)

	// ------------------------------------------------------------
	// Health
	// ------------------------------------------------------------

	mux.HandleFunc("GET /healthz",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		},
	)

	// ------------------------------------------------------------
	// Debug
	// ------------------------------------------------------------

	mux.HandleFunc("/__debug_unmatched__",
		func(w http.ResponseWriter, r *http.Request) {
			log.Printf("UNMATCHED DEBUG PATH=%s Host=%s Remote=%s", r.URL.Path, r.Host, r.RemoteAddr)

			http.NotFound(w, r)
		},
	)

	// 先挂载二级路径
	handler := mount(cfg.Subfolder, mux)

	// 最外层加认证
	return auth.Middleware(handler)
}

// 给整个 HTTP 服务挂载一个访问前缀
func mount(subfolder string, handler http.Handler) http.Handler {
	if subfolder == "/" || subfolder == "" {
		return handler
	}

	// 去掉最后的 /
	prefix := strings.TrimSuffix(subfolder, "/")

	// 创建一个新的外层路由
	mux := http.NewServeMux()
	// 接收二级路径，然后剥离掉这个前缀，再把剩余路径交给原来的 handler
	mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))

	mux.HandleFunc(prefix,
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		},
	)

	return mux
}

// New 构造一个反向代理 handler，把请求（含 WebSocket）透传到 target。
// target 形如 "https://127.0.0.1:6901"，解析失败返回 error。
func newVNCProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse target: %w", err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.SetXForwarded()
		},
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   100,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // KasmVNC 本地自签名，仅限可信回环
			},
			ForceAttemptHTTP2: false, // WebSocket 依赖 HTTP/1.1 Upgrade
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}

	return proxy, nil
}

// 将服务器上的文件作为 HTTP 响应返回给浏览器
func staticFile(path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, path)
	}
}

// 模板渲染完整 HTML，成功发给浏览器，失败返回 500
func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data pageData) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
