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
	if err := os.RemoveAll(socket); err != nil {
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
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(baseDir, "public", "index.html")))
	manifestTmpl := template.Must(template.ParseFiles(filepath.Join(baseDir, "public", "manifest.json")))

	files := &filesHub{
		root:          cleanRoot(cfg.FMHome),
		maxUploadSize: cfg.MaxUploadSize,
	}
	audio := newAudioHub(cfg.Audio.Device, cfg.Audio.Server, cfg.MicSocket)
	vncProxy := newVNCProxy(cfg.VNC.ProxyTarget)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		renderTemplate(
			w,
			indexTmpl,
			pageData{
				Title: cfg.Title,
				Path:  cfg.VNCPath(),
			},
			"text/html; charset=utf-8",
		)

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
	mux.Handle("/public/", http.StripPrefix("/public", http.FileServer(http.Dir(filepath.Join(baseDir, "public")))))

	// Proxy /vnc/ requests to the upstream KasmVNC server so the browser
	// sees the real KasmVNC UI and any authentication challenges (e.g.
	// Basic WWW-Authenticate) rather than serving static files locally.
	if targetURL, err := url.Parse(cfg.VNC.ProxyTarget); err == nil {
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		originalDirector := proxy.Director
		proxy.Director = func(r *http.Request) {
			originalDirector(r)
			// Ensure Host is set to upstream so upstream's virtual host and
			// cookie domains match expectations.
			r.Host = targetURL.Host
		}
		// Log upstream responses (status and cookie names) for debugging so we
		// can see if KasmVNC sets session cookies required for WebSocket auth.
		proxy.ModifyResponse = func(resp *http.Response) error {
			cookieNames := []string{}
			for _, c := range resp.Cookies() {
				cookieNames = append(cookieNames, c.Name)
			}
			log.Printf("vnc proxy upstream resp path=%s status=%d set_cookies=%v", resp.Request.URL.Path, resp.StatusCode, cookieNames)
			return nil
		}
		// Allow communicating with upstream TLS targets that use self-signed
		// certs in local test environments by skipping verification here,
		// matching dialUpstream's InsecureSkipVerify behavior.
		proxy.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		mux.HandleFunc("/vnc/", func(w http.ResponseWriter, r *http.Request) {
			// Rewrite the request path to remove the /vnc prefix so it maps to
			// the upstream resource paths.
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/vnc")
			proxy.ServeHTTP(w, r)
		})
	} else {
		mux.Handle("/vnc/", http.StripPrefix("/vnc", http.FileServer(http.Dir(cfg.VNC.Dir))))
	}
	mux.HandleFunc("/websockify", vncProxy.ServeHTTP)
	mux.HandleFunc("/websockify/", vncProxy.ServeHTTP)
	mux.HandleFunc("GET /files/ws", files.handleWS)
	mux.HandleFunc("GET /audio/ws", audio.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Catch-all debug route to help diagnose unmatched paths when testing.
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
