// Kclient is a Go wrapper for KasmVNC with file-management and PCM audio bridges.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const maxMessageSize = 200 * 1024 * 1024

type config struct {
	listenAddr, subfolder, title, fileRoot, vncRoot, micPath, pulseCommand string
}

type pageData struct{ Title, VNCPath string }
type manifestData struct{ Title string }

type message struct {
	Type      string   `json:"type"`
	Directory string   `json:"directory,omitempty"`
	Path      string   `json:"path,omitempty"`
	Name      string   `json:"name,omitempty"`
	Dirs      []string `json:"dirs,omitempty"`
	Files     []string `json:"files,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type client struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *client) sendJSON(v message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}
func (c *client) sendBinary(v []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, v)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // same-origin deployment; enforce origin at proxy/auth layer if cross-origin is enabled
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	s := server{cfg: cfg}
	http.Handle("/", s.routes())
	log.Printf("kclient listening on %s%s", cfg.listenAddr, cfg.subfolder)
	log.Fatal(http.ListenAndServe(cfg.listenAddr, nil))
}

func loadConfig() (config, error) {
	root := env("FM_HOME", "/config")
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		listenAddr: env("LISTEN_ADDR", ":6900"), subfolder: normalizePrefix(env("SUBFOLDER", "/")),
		title: env("TITLE", "KasmVNC Client"), fileRoot: filepath.Clean(absRoot),
		vncRoot:      env("KASMVNC_WEB_ROOT", "/usr/share/kasmvnc/www"),
		micPath:      env("MIC_PATH", "/defaults/mic.sock"),
		pulseCommand: env("PULSE_RECORD_COMMAND", "parec --device=auto_null.monitor --format=s16le --rate=44100 --channels=2"),
	}
	if err := os.MkdirAll(cfg.fileRoot, 0o755); err != nil {
		return config{}, fmt.Errorf("create FM_HOME: %w", err)
	}
	return cfg, nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func normalizePrefix(value string) string {
	if value == "" || value == "/" {
		return "/"
	}
	return "/" + strings.Trim(value, "/") + "/"
}

type server struct{ cfg config }

func (s server) routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := s.relativePath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch {
		case path == "/":
			s.home(w, r)
		case path == "/manifest.json":
			s.manifest(w, r)
		case path == "/files":
			http.ServeFile(w, r, filepath.Join("public", "filebrowser.html"))
		case path == "/files/ws":
			s.fileWS(w, r)
		case path == "/audio/ws":
			s.audioWS(w, r)
		case strings.HasPrefix(path, "/public/"):
			http.StripPrefix(s.url("/public/"), http.FileServer(http.Dir("public"))).ServeHTTP(w, r)
		case strings.HasPrefix(path, "/vnc/"):
			http.StripPrefix(s.url("/vnc/"), http.FileServer(http.Dir(s.cfg.vncRoot))).ServeHTTP(w, r)
		case path == "/favicon.ico":
			http.ServeFile(w, r, filepath.Join("public", "favicon.ico"))
		default:
			http.NotFound(w, r)
		}
	})
}
func (s server) relativePath(requestPath string) (string, bool) {
	if s.cfg.subfolder == "/" {
		return requestPath, true
	}
	base := strings.TrimSuffix(s.cfg.subfolder, "/")
	if requestPath == base {
		return "/", true
	}
	if !strings.HasPrefix(requestPath, s.cfg.subfolder) {
		return "", false
	}
	return "/" + strings.TrimPrefix(requestPath, s.cfg.subfolder), true
}
func (s server) url(path string) string {
	if s.cfg.subfolder == "/" {
		return path
	}
	return strings.TrimSuffix(s.cfg.subfolder, "/") + path
}
func (s server) home(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles(filepath.Join("public", "index.html"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	vncPath := ""
	if s.cfg.subfolder != "/" {
		vncPath = "&path=" + strings.Trim(s.cfg.subfolder, "/") + "/websockify"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, pageData{Title: s.cfg.title, VNCPath: vncPath})
}
func (s server) manifest(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles(filepath.Join("public", "manifest.json"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = t.Execute(w, manifestData{Title: s.cfg.title})
}

func (s server) fileWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxMessageSize)
	c := &client{conn: conn}
	var uploadPath string
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage {
			if uploadPath == "" {
				_ = c.sendJSON(message{Type: "error", Error: "unexpected binary payload"})
				continue
			}
			if err := s.writeFile(uploadPath, payload); err != nil {
				_ = c.sendJSON(message{Type: "error", Error: err.Error()})
			} else {
				_ = s.renderFiles(c, parentPath(uploadPath))
			}
			uploadPath = ""
			continue
		}
		var req message
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = c.sendJSON(message{Type: "error", Error: "invalid JSON request"})
			continue
		}
		switch req.Type {
		case "open":
			_ = s.renderFiles(c, "/")
		case "getfiles":
			_ = s.renderFiles(c, req.Directory)
		case "download":
			_ = s.download(c, req.Path)
		case "upload":
			if _, err := s.safePath(req.Path, false); err != nil {
				_ = c.sendJSON(message{Type: "error", Error: err.Error()})
			} else {
				uploadPath = req.Path
			}
		case "delete":
			if err := s.deletePath(req.Path); err != nil {
				_ = c.sendJSON(message{Type: "error", Error: err.Error()})
			} else {
				_ = s.renderFiles(c, req.Directory)
			}
		case "mkdir":
			if err := s.makeDir(req.Path); err != nil {
				_ = c.sendJSON(message{Type: "error", Error: err.Error()})
			} else {
				_ = s.renderFiles(c, req.Directory)
			}
		default:
			_ = c.sendJSON(message{Type: "error", Error: "unknown request type"})
		}
	}
}

func (s server) renderFiles(c *client, dir string) error {
	path, err := s.safePath(dir, true)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	dirs, files := make([]string, 0), make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		} else {
			files = append(files, entry.Name())
		}
	}
	return c.sendJSON(message{Type: "renderfiles", Directory: cleanVirtual(dir), Dirs: dirs, Files: files})
}
func (s server) download(c *client, virtual string) error {
	path, err := s.safePath(virtual, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("cannot download a directory")
	}
	if info.Size() > maxMessageSize {
		return errors.New("file exceeds 200 MB download limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := c.sendJSON(message{Type: "download", Name: filepath.Base(path)}); err != nil {
		return err
	}
	return c.sendBinary(data)
}
func (s server) writeFile(virtual string, data []byte) error {
	path, err := s.safePath(virtual, false)
	if err != nil {
		return err
	}
	if len(data) > maxMessageSize {
		return errors.New("file exceeds 200 MB upload limit")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := s.safePath(virtual, false); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
func (s server) deletePath(virtual string) error {
	if cleanVirtual(virtual) == "/" {
		return errors.New("cannot delete file root")
	}
	path, err := s.safePath(virtual, true)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}
func (s server) makeDir(virtual string) error {
	if cleanVirtual(virtual) == "/" {
		return errors.New("cannot create file root")
	}
	path, err := s.safePath(virtual, false)
	if err != nil {
		return err
	}
	return os.Mkdir(path, 0o755)
}
func (s server) safePath(virtual string, mustExist bool) (string, error) {
	clean := cleanVirtual(virtual)
	path := filepath.Join(s.cfg.fileRoot, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	rel, err := filepath.Rel(s.cfg.fileRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path is outside file root")
	}
	parentReal, err := existingAncestor(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	parentRel, err := filepath.Rel(s.cfg.fileRoot, parentReal)
	if err != nil || parentRel == ".." || strings.HasPrefix(parentRel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path parent is outside file root")
	}
	if mustExist {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symbolic links are not allowed")
		}
	}
	return path, nil
}
func cleanVirtual(value string) string {
	return "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+value)), "/")
}
func parentPath(value string) string {
	return cleanVirtual(filepath.ToSlash(filepath.Dir(cleanVirtual(value))))
}

// existingAncestor resolves the nearest existing parent before creating a path.
// This permits nested drag-and-drop uploads while preventing symlink escapes.
func existingAncestor(path string) (string, error) {
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

func (s server) audioWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxMessageSize)
	c := &client{conn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stop func()
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			if stop != nil {
				stop()
			}
			return
		}
		if kind == websocket.BinaryMessage {
			if err := os.WriteFile(s.cfg.micPath, payload, 0o600); err != nil {
				_ = c.sendJSON(message{Type: "error", Error: err.Error()})
			}
			continue
		}
		var req message
		if json.Unmarshal(payload, &req) != nil {
			continue
		}
		switch req.Type {
		case "open":
			if stop == nil {
				stop = s.streamAudio(ctx, c)
			}
		case "close":
			if stop != nil {
				stop()
				stop = nil
			}
		}
	}
}
func (s server) streamAudio(ctx context.Context, c *client) func() {
	ctx, cancel := context.WithCancel(ctx)
	args := strings.Fields(s.cfg.pulseCommand)
	if len(args) == 0 {
		return cancel
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = c.sendJSON(message{Type: "error", Error: err.Error()})
		return cancel
	}
	if err := cmd.Start(); err != nil {
		_ = c.sendJSON(message{Type: "error", Error: "audio unavailable: " + err.Error()})
		return cancel
	}
	go func() {
		defer cmd.Wait()
		buffer := make([]byte, 8192)
		for {
			n, err := stdout.Read(buffer)
			if n > 0 && !allZero(buffer[:n]) {
				if c.sendBinary(buffer[:n]) != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("audio stream: %v", err)
				}
				return
			}
		}
	}()
	return cancel
}
func allZero(data []byte) bool {
	for _, v := range data {
		if v != 0 {
			return false
		}
	}
	return true
}

func init() { log.SetFlags(log.LstdFlags | log.LUTC) }
