package main

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
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

	listener, err := createListener(cfg)
	if err != nil {
		log.Fatalf("create listener: %v", err)
	}

	// 优雅退出
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		log.Printf("kclient server started: %s", listenerDescription(cfg))

		if err := serve(server, listener, cfg); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")

	case err := <-errCh:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}

		return
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

	// Unix Socket 文件需要清理。
	if cfg.Socket != "" {
		if err := os.Remove(cfg.Socket); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			log.Printf(
				"remove unix socket %s: %v",
				cfg.Socket,
				err,
			)
		}
	}

	log.Println("kclient stopped")
}

// createListener 根据配置选择：
//
//  1. Unix Socket
//  2. TCP listener（HTTPS）
func createListener(cfg Config) (net.Listener, error) {
	if cfg.Socket != "" {
		// 删除旧 socket。
		if err := os.Remove(cfg.Socket); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		listener, err := net.Listen("unix", cfg.Socket)
		if err != nil {
			return nil, err
		}

		// 允许 nginx / fnOS gateway 访问。
		if err := os.Chmod(cfg.Socket, 0660); err != nil {
			_ = listener.Close()
			return nil, err
		}

		return listener, nil
	}

	addr := ":" + strconv.Itoa(cfg.VNC.Port)
	return net.Listen("tcp", addr)
}

// serve 根据配置决定 HTTP Server 的工作方式。
//
// Unix Socket：
//
//	Serve(listener)
//
// HTTPS：
//
//	ServeTLS(listener, certFile, keyFile)
//
// 这样 handler 始终只有一份。
func serve(server *http.Server, listener net.Listener, cfg Config) error {
	if cfg.Socket != "" {
		log.Printf("kclient listening on unix socket: %s", cfg.Socket)
		return server.Serve(listener)
	}

	certFile := cfg.SSL.CertFile
	keyFile := cfg.SSL.KeyFile

	if certFile == "" {
		return errors.New("HTTPS certificate file is empty")
	}

	if keyFile == "" {
		return errors.New("HTTPS private key file is empty")
	}
	log.Printf("kclient HTTPS listening on :%d", cfg.VNC.Port)

	return server.ServeTLS(listener, certFile, keyFile)
}

func listenerDescription(cfg Config) string {
	if cfg.Socket != "" {
		return "unix://" + cfg.Socket
	}

	return "https://0.0.0.0:" + strconv.Itoa(cfg.VNC.Port)
}

func newServer(cfg Config, baseDir string) http.Handler {
	publicDir := filepath.Join(baseDir, "public")
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "index.html")))
	manifestTmpl := template.Must(template.ParseFiles(filepath.Join(publicDir, "manifest.json")))

	files := &filesHub{root: cleanRoot(cfg.FMHome), maxUploadSize: cfg.MaxUploadSize}

	audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)

	// ------------------------------------------------------------
	// KasmVNC ReverseProxy
	// ------------------------------------------------------------

	vncProxy, err := newVNCProxy(cfg.VNC.ProxyTarget)
	if err != nil {
		log.Fatalf("create KasmVNC proxy: %v", err)
	}

	mux := http.NewServeMux()

	// ------------------------------------------------------------
	// 首页
	// ------------------------------------------------------------
	mux.HandleFunc("GET /{$}",
		func(w http.ResponseWriter, r *http.Request) {
			log.Printf("render index: subfolder=%q vncPath=%q", cfg.Subfolder, cfg.VNCPath())
			renderTemplate(w, indexTmpl, pageData{Title: cfg.Title, Path: cfg.VNCPath()}, "text/html; charset=utf-8")
		},
	)

	// ------------------------------------------------------------
	// Manifest
	// ------------------------------------------------------------
	mux.HandleFunc("GET /manifest.json",
		func(w http.ResponseWriter, r *http.Request) {
			renderTemplate(w, manifestTmpl, pageData{Title: cfg.Title}, "application/json; charset=utf-8")
		},
	)

	// ------------------------------------------------------------
	// Favicon
	// ------------------------------------------------------------
	mux.HandleFunc("GET /favicon.ico",
		func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(publicDir, "favicon.ico"))
		},
	)

	// ------------------------------------------------------------
	// File Manager
	// ------------------------------------------------------------
	mux.HandleFunc("GET /files",
		func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(publicDir, "filebrowser.html"))
		},
	)

	mux.HandleFunc("GET /files/",
		func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(publicDir, "filebrowser.html"))
		},
	)

	// ------------------------------------------------------------
	// Static
	// ------------------------------------------------------------
	mux.Handle("/public/", http.StripPrefix("/public", http.FileServer(http.Dir(publicDir))))

	// ------------------------------------------------------------
	// KasmVNC
	//
	// /vnc/index.html
	//     ↓
	// https://127.0.0.1:6901/index.html
	// ------------------------------------------------------------

	mux.Handle("/vnc/", http.StripPrefix("/vnc", vncProxy))

	// ------------------------------------------------------------
	// KasmVNC WebSocket
	//
	// /websockify
	// /websockify/*
	// ------------------------------------------------------------

	mux.Handle("/websockify", vncProxy)
	mux.Handle("/websockify/", vncProxy)

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

	mux.HandleFunc(prefix,
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		},
	)

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
