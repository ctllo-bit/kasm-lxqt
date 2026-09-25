package router

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
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
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type pageData struct {
	Title string
	Path  string
}

const kclientDir = "/var/apps/kasm-lxqt/target/kclient"

func NewHandler(cfg config.Config, authenticator *auth.Authenticator) http.Handler {
	// 根目录资源
	publicDir := filepath.Join(kclientDir, "public")
	// 加载 index.html 模板
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "index.html")))
	// 加载 login.html 登陆模板
	loginTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "login.html")))

	sessionStore := auth.NewSessionStore()

	//files := &filesHub{root: cleanRoot(cfg.FMHome), maxUploadSize: cfg.MaxUploadSize}
	//audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)

	// ------------------------------------------------------------
	// KasmVNC ReverseProxy
	// ------------------------------------------------------------
	vncProxy, err := newVNCProxy(cfg.VNCProxyTarget)
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
	mux.Handle("/vnc/", http.StripPrefix("/vnc", http.FileServer(http.Dir("/usr/share/kasmvnc/www/"))))

	// manifest / favicon
	mux.HandleFunc("/manifest.json", staticFile(filepath.Join(publicDir, "manifest.json"), "application/manifest+json"))
	mux.HandleFunc("/favicon.ico", staticFile(filepath.Join(publicDir, "favicon.ico"), "image/x-icon"))

	// ------------------------------------------------------------
	// 登陆页面：不需要 session
	// ------------------------------------------------------------
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, loginTmpl, pageData{Title: cfg.Title, Path: cfg.ResolvePath("/login")})
	})

	mux.HandleFunc("POST /login",
		func(w http.ResponseWriter, r *http.Request) {

			username := r.FormValue("username")
			password := r.FormValue("password")

			// 验证 .kasmpasswd
			if !authenticator.Verify(username, password) {
				http.Error(
					w,
					"用户名或密码错误",
					http.StatusUnauthorized,
				)
				return
			}

			// 生成 Authorization: Basic xxx
			raw := username + ":" + password

			authorization := "Basic " +
				base64.StdEncoding.EncodeToString(
					[]byte(raw),
				)

			// 创建 Session
			sessionID, err := sessionStore.Create(authorization)
			if err != nil {
				http.Error(
					w,
					"failed to create session",
					http.StatusInternalServerError,
				)
				return
			}

			// 保存 Session Cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "kclient_session",
				Value:    sessionID,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
			})

			// 登录成功
			http.Redirect(w, r, "/", http.StatusSeeOther)
		},
	)

	// ------------------------------------------------------------
	// 首页：需要 session
	// ------------------------------------------------------------
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {

		if _, ok := sessionStore.GetFromRequest(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		renderTemplate(w, indexTmpl, pageData{Title: cfg.Title, Path: cfg.ResolvePath("/websockify")})
	})

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

	// 根据配置决定是否启用浏览器认证,最外层加认证
	if cfg.Mode == "port" {
		return sessionStore.Middleware(handler)
	}

	return handler
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
			// 转发到 KasmVNC
			r.SetURL(u)
			// Host 改成 KasmVNC
			r.Out.Host = u.Host
			// 添加 X-Forwarded-*
			r.SetXForwarded()
		},
		Transport: &http.Transport{
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

// 初始化创建 Listener
func CreateListener(cfg config.Config) (net.Listener, error) {
	switch cfg.Mode {
	// ============================================================
	// Gateway 模式
	// 飞牛统一网关 -> Unix Socket -> kclient
	// ============================================================
	case "gateway":
		if cfg.Listen.Socket == "" {
			return nil, errors.New("gateway mode: listen.socket is empty")
		}

		socketPath := cfg.Listen.Socket

		// 删除启动前残留的旧 Socket
		if err := os.Remove(socketPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove old unix socket %s: %w", socketPath, err)
		}

		// 创建 Unix Socket
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("listen unix socket %s: %w", socketPath, err)
		}

		// 设置 Socket 权限
		if err := os.Chmod(socketPath, 0660); err != nil {
			_ = listener.Close()
			_ = os.Remove(socketPath)

			return nil, fmt.Errorf("chmod unix socket %s: %w", socketPath, err)
		}

		return listener, nil

	// ============================================================
	// Port 模式
	// 浏览器 -> TCP -> kclient
	// ============================================================
	case "port":
		if cfg.Listen.Port <= 0 || cfg.Listen.Port > 65535 {
			return nil, fmt.Errorf("port mode: invalid listen.port %d", cfg.Listen.Port)
		}
		addr := ":" + strconv.Itoa(cfg.Listen.Port)

		listener, err := net.Listen("tcp", addr)

		if err != nil {
			return nil, fmt.Errorf("listen tcp %s: %w", addr, err)
		}

		return listener, nil
	default:
		return nil, fmt.Errorf("invalid mode %q, expected gateway or port", cfg.Mode)
	}

}
