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

	socket := cfg.Socket

	// 清理旧 socket
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("remove old socket: %v", err)
	}

	listener, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatalf("listen unix socket %s: %v", socket, err)
	}
	defer os.Remove(socket)

	// 给 nginx/fnOS gateway访问
	if err := os.Chmod(socket, 0660); err != nil {
		log.Printf("chmod socket: %v", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 优雅退出
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)

		<-ch
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("kclient socket: %s", socket)

	if err := server.Serve(listener); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newServer(cfg Config, baseDir string) http.Handler {
	indexTmpl := template.Must(
		template.ParseFiles(filepath.Join(baseDir, "public", "index.html")),
	)
	manifestTmpl := template.Must(
		template.ParseFiles(filepath.Join(baseDir, "public", "manifest.json")),
	)

	files := &filesHub{
		root:          cleanRoot(cfg.FMHome),
		maxUploadSize: cfg.MaxUploadSize,
	}

	audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)

	mux := http.NewServeMux()

	// ------------------------------------------------------------
	// KClient 自己的页面
	// ------------------------------------------------------------

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("render index: subfolder=%q vncPath=%q", cfg.Subfolder, cfg.VNCPath())
		renderTemplate(w, indexTmpl, pageData{Title: cfg.Title, Path: cfg.VNCPath()}, "text/html; charset=utf-8")
	})
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, manifestTmpl, pageData{Title: cfg.Title}, "application/json; charset=utf-8")
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(baseDir, "public", "favicon.ico"))
	})
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(baseDir, "public", "filebrowser.html"))
	})
	mux.HandleFunc("GET /files/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(baseDir, "public", "filebrowser.html"))
	})

	mux.Handle(
		"/public/",
		http.StripPrefix(
			"/public",
			http.FileServer(
				http.Dir(filepath.Join(baseDir, "public")),
			),
		),
	)

	// ------------------------------------------------------------
	// KasmVNC Reverse Proxy
	// ------------------------------------------------------------

	targetURL, err := url.Parse(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("invalid VNC proxy target %q: %v", cfg.VNC.ProxyTarget, err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			in := pr.In
			out := pr.Out

			// 保留客户端全部 Header，包括：
			// Authorization / Cookie / Origin / Upgrade / Sec-WebSocket-*
			out.Header = in.Header.Clone()

			out.URL.Scheme = targetURL.Scheme
			out.URL.Host = targetURL.Host
			out.URL.Path = in.URL.Path
			out.URL.RawPath = ""
			out.URL.RawQuery = in.URL.RawQuery

			// 上游 Host
			out.Host = targetURL.Host

			log.Printf(
				"KasmVNC OUT: method=%s path=%s query=%q upgrade=%q connection=%q",
				in.Method,
				in.URL.Path,
				in.URL.RawQuery,
				in.Header.Get("Upgrade"),
				in.Header.Get("Connection"),
			)
		},

		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 仅用于本机 KasmVNC 自签名证书
			},
		},

		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 400 {
				log.Printf("KasmVNC response error: path=%s status=%d", resp.Request.URL.Path, resp.StatusCode)
			}
			return nil
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("KasmVNC proxy error: method=%s path=%s err=%v", r.Method, r.URL.Path, err)
			http.Error(w, "KasmVNC upstream unavailable", http.StatusBadGateway)
		},
	}

	// ------------------------------------------------------------
	// /vnc/* -> https://127.0.0.1:6901/*
	// ------------------------------------------------------------

	mux.HandleFunc("/vnc/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/vnc")

		proxy.ServeHTTP(w, r)
	})

	// ------------------------------------------------------------
	// /websockify -> https://127.0.0.1:6901/websockify
	// ------------------------------------------------------------

	websocketProxy := func(w http.ResponseWriter, r *http.Request) {
		log.Printf(
			"WEBSOCKET IN: method=%s path=%s host=%s upgrade=%q connection=%q sec-websocket-key=%t origin=%q",
			r.Method,
			r.URL.Path,
			r.Host,
			r.Header.Get("Upgrade"),
			r.Header.Get("Connection"),
			r.Header.Get("Sec-WebSocket-Key") != "",
			r.Header.Get("Origin"),
		)

		proxy.ServeHTTP(w, r)
	}

	mux.HandleFunc("/websockify", websocketProxy)
	mux.HandleFunc("/websockify/", websocketProxy)

	// ------------------------------------------------------------
	// File Manager WebSocket
	// ------------------------------------------------------------

	mux.HandleFunc("GET /files/ws", files.handleWS)

	// ------------------------------------------------------------
	// Audio WebSocket
	// ------------------------------------------------------------

	mux.HandleFunc("GET /audio/ws", audio.handleWS)

	// ------------------------------------------------------------
	// Health
	// ------------------------------------------------------------

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ------------------------------------------------------------
	// Debug
	// ------------------------------------------------------------

	mux.HandleFunc("/__debug_unmatched__", func(w http.ResponseWriter, r *http.Request) {
		log.Printf(
			"UNMATCHED DEBUG PATH=%s Host=%s Remote=%s",
			r.URL.Path,
			r.Host,
			r.RemoteAddr,
		)

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
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return filepath.Clean(root)
	}
	return abs
}

// resolveBaseDir finds the directory containing the public assets. It prefers
// the executable location so the binary can be run from any working directory,
// and falls back to the current directory for `go run` development.
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
