package main

import (
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type pageData struct {
	Title string
	Path  string
}

func main() {
	cfg := loadConfig()
	baseDir := resolveBaseDir()

	handler := newServer(cfg, baseDir)

	log.Printf("kclient listening on port %d (subfolder %q, file root %s)", cfg.Port, cfg.Subfolder, cleanRoot(cfg.FMHome))
	if err := http.ListenAndServe(":"+strconv.Itoa(cfg.Port), handler); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func newServer(cfg Config, baseDir string) http.Handler {
	indexTmpl := template.Must(template.ParseFiles(filepath.Join(baseDir, "public", "index.html")))
	manifestTmpl := template.Must(template.ParseFiles(filepath.Join(baseDir, "public", "manifest.json")))

	files := &filesHub{
		root:          cleanRoot(cfg.FMHome),
		maxUploadSize: cfg.MaxUploadSize,
	}
	audio := newAudioHub(cfg.AudioDevice, cfg.AudioServer, cfg.MicSocket)
	vncProxy := newVNCProxy(cfg.VNCProxyTarget, cfg.CustomUser, cfg.Password)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
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
	mux.Handle("GET /public/", http.StripPrefix("/public", http.FileServer(http.Dir(filepath.Join(baseDir, "public")))))
	mux.Handle("GET /vnc/", http.StripPrefix("/vnc", http.FileServer(http.Dir(cfg.VNCDir))))
	mux.HandleFunc("/websockify", vncProxy.ServeHTTP)
	mux.HandleFunc("/websockify/", vncProxy.ServeHTTP)
	mux.HandleFunc("GET /files/ws", files.handleWS)
	mux.HandleFunc("GET /audio/ws", audio.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
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
