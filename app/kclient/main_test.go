package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHTTPServerRoutes(t *testing.T) {
	root := testRoot(t)
	cfg := Config{
		Title:     "Test Title",
		Subfolder: "/kclient",
		FMHome:    root,
		VNCDir:    filepath.Join(t.TempDir(), "missing"),
	}
	srv := httptest.NewServer(newServer(cfg, "."))
	defer srv.Close()

	index := getBody(t, srv.URL+"/kclient/")
	if !strings.Contains(index, "Test Title") {
		t.Fatalf("index page missing title: %s", index)
	}
	if !strings.Contains(index, "vnc/index.html") {
		t.Fatalf("index page missing vnc iframe: %s", index)
	}

	manifest := getBody(t, srv.URL+"/kclient/manifest.json")
	if !strings.Contains(manifest, `"name": "Test Title"`) {
		t.Fatalf("manifest missing title: %s", manifest)
	}

	files := getBody(t, srv.URL+"/kclient/files")
	if !strings.Contains(files, "filebrowser.js") {
		t.Fatalf("file browser page missing script: %s", files)
	}
	filesSlash := getBody(t, srv.URL+"/kclient/files/")
	if !strings.Contains(filesSlash, "filebrowser.js") {
		t.Fatalf("file browser page with trailing slash missing script: %s", filesSlash)
	}

	if got := getBody(t, srv.URL+"/kclient/healthz"); got != "ok" {
		t.Fatalf("healthz returned %q", got)
	}
}

func TestFileBrowserWebSocket(t *testing.T) {
	root := testRoot(t)
	cfg := Config{
		Subfolder:     "/kclient",
		FMHome:        root,
		MaxUploadSize: 200000000,
	}
	srv := httptest.NewServer(newServer(cfg, "."))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/kclient/files/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	sendText(t, conn, `{"type":"open"}`)
	initial := readText(t, conn)
	if initial["type"] != "renderfiles" {
		t.Fatalf("expected renderfiles after open, got %v", initial)
	}
	if len(initial["files"].([]any)) != 0 || len(initial["dirs"].([]any)) != 0 {
		t.Fatalf("expected empty root listing, got %v", initial)
	}

	payload := []byte("hello from go")
	sendCommand(t, conn, map[string]any{
		"type":      "upload",
		"directory": root,
		"path":      filepath.Join(root, "hello.txt"),
		"render":    true,
	})
	sendBinary(t, conn, payload)
	ack := readText(t, conn)
	if ack["type"] != "upload-ack" {
		t.Fatalf("expected upload-ack, got %v", ack)
	}
	readText(t, conn) // renderfiles after upload

	written, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("uploaded content mismatch: %q", written)
	}

	sendCommand(t, conn, map[string]any{
		"type": "download",
		"file": filepath.Join(root, "hello.txt"),
	})
	downloadMeta := readText(t, conn)
	if downloadMeta["type"] != "download" || downloadMeta["name"] != "hello.txt" {
		t.Fatalf("unexpected download metadata: %v", downloadMeta)
	}
	downloaded := readBinary(t, conn)
	if string(downloaded) != string(payload) {
		t.Fatalf("downloaded content mismatch: %q", downloaded)
	}

	sendCommand(t, conn, map[string]any{"type": "getfiles", "directory": root})
	listing := readText(t, conn)
	if !containsString(listing["files"].([]any), "hello.txt") {
		t.Fatalf("file not listed after upload: %v", listing)
	}

	sendCommand(t, conn, map[string]any{
		"type":      "createfolder",
		"dir":       filepath.Join(root, "newdir"),
		"directory": root,
	})
	readText(t, conn)
	if info, err := os.Stat(filepath.Join(root, "newdir")); err != nil || !info.IsDir() {
		t.Fatalf("created directory missing: %v", err)
	}

	sendCommand(t, conn, map[string]any{
		"type":      "delete",
		"item":      filepath.Join(root, "hello.txt"),
		"directory": root,
	})
	readText(t, conn)
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file still exists: %v", err)
	}

	sendCommand(t, conn, map[string]any{"type": "getfiles", "directory": filepath.Dir(root)})
	escaped := readText(t, conn)
	if escaped["type"] != "error" {
		t.Fatalf("expected path escape to be rejected, got %v", escaped)
	}
}

func TestVNCPath(t *testing.T) {
	if got := (Config{Subfolder: "/"}).VNCPath(); got != "" {
		t.Fatalf("root subfolder path should be empty, got %q", got)
	}
	if got := (Config{Subfolder: "/kclient"}).VNCPath(); got != "&path=kclient/websockify" {
		t.Fatalf("unexpected vnc path: %q", got)
	}
}

func TestPulseServerEnv(t *testing.T) {
	cases := map[string]string{
		"/run/user/1000/pulse/native":      "unix:/run/user/1000/pulse/native",
		"unix:/run/user/1000/pulse/native": "unix:/run/user/1000/pulse/native",
		"tcp:localhost":                    "tcp:localhost",
		"localhost":                        "localhost",
	}
	for input, want := range cases {
		if got := pulseServerEnv(input); got != want {
			t.Fatalf("pulseServerEnv(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVNCWebSocketProxy(t *testing.T) {
	var mu sync.Mutex
	var authSeen string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authSeen = r.Header.Get("Authorization")
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upstream upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, data); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	cfg := Config{
		Subfolder:      "/kclient",
		FMHome:         testRoot(t),
		VNCDir:         filepath.Join(t.TempDir(), "missing"),
		VNCProxyTarget: upstream.URL,
		CustomUser:     "abc",
		Password:       "123456",
	}
	srv := httptest.NewServer(newServer(cfg, "."))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/kclient/websockify"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial proxied websocket: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write through proxy: %v", err)
	}
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read through proxy: %v", err)
	}
	if messageType != websocket.TextMessage || string(data) != "ping" {
		t.Fatalf("unexpected echo: type=%d data=%q", messageType, data)
	}

	mu.Lock()
	auth := authSeen
	mu.Unlock()
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("abc:123456"))
	if auth != wantAuth {
		t.Fatalf("upstream saw %q, want %q", auth, wantAuth)
	}
}

func sendText(t *testing.T, conn *websocket.Conn, message string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		t.Fatalf("send text: %v", err)
	}
}

func sendCommand(t *testing.T, conn *websocket.Conn, message any) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	sendText(t, conn, string(data))
}

func sendBinary(t *testing.T, conn *websocket.Conn, data []byte) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("send binary: %v", err)
	}
}

func readText(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("expected text message, got type %d", messageType)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	return msg
}

func readBinary(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("expected binary message, got type %d", messageType)
	}
	return data
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

func containsString(items []any, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func testRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".kclient-test-")
	if err != nil {
		t.Fatalf("create test root: %v", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve test root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
