package router

import (
	"bytes"
	"html/template"
	"kclient/config"
	"kclient/internal/audio"
	"kclient/internal/auth"
	"log"
	"net/http"
)

type pageData struct {
	Title   string
	VNCPath string
}

// New 组装所有路由，返回最终 handler（含认证中间件）。
func New(cfg config.Config, a *auth.Authenticator, vncProxy http.Handler) (http.Handler, error) {
	// 启动时解析一次
	tmpl, err := template.ParseFiles("./public/index.html")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	// KasmVNC 页面和静态资源
	mux.Handle("/vnc/", http.StripPrefix("/vnc/", vncProxy))

	// 首页 + 兜底代理：非 "/" 的请求全部透传给 KasmVNC，这样 /assets/*、/app/*、/websockify 都能到达上游
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			renderIndex(w, tmpl, cfg) // 传入已解析的模板
			return
		}
		vncProxy.ServeHTTP(w, r)
	})

	// Audio WebRTC
	mux.HandleFunc("POST /audio/offer", audio.HandleOffer)

	// 本地静态资源
	mux.Handle("/public/", http.StripPrefix("/public/", http.FileServer(http.Dir("./public"))))

	// manifest / favicon
	mux.HandleFunc("/manifest.json", staticFile("./public/manifest.json", "application/manifest+json"))
	mux.HandleFunc("/favicon.ico", staticFile("./public/favicon.ico", "image/x-icon"))

	// 认证包在最外层
	return a.Middleware(mux), nil
}

// 先在内存里把模板渲染成完整 HTML，成功才发给浏览器，失败返回 500
func renderIndex(w http.ResponseWriter, tmpl *template.Template, cfg config.Config) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, pageData{
		Title:   cfg.Title,
		VNCPath: cfg.VNCPath(),
	}); err != nil {
		log.Printf("render index: %v", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func staticFile(path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, path)
	}
}
