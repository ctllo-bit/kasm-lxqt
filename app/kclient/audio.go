package main

import (
	"bytes"
	"encoding/json"
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

const (
	audioChunkSize       = 4096
	audioBroadcastBuffer = 64
	audioClientBuffer    = 8
)

type audioHub struct {
	device       string
	server       string
	micSocket    string
	audioEnabled bool

	mu        sync.RWMutex
	clients   map[*audioClient]struct{}
	broadcast chan []byte

	sourceMu sync.Mutex
	source   *audioSource
}

func newAudioHub(device, server, micSocket string) *audioHub {
	_, err := exec.LookPath("parec")
	enabled := err == nil
	if !enabled {
		log.Printf("kclient audio disabled: parec not found on PATH (%v)", err)
	}
	if server == "" {
		server = detectPulseServer()
	}
	if server != "" {
		log.Printf("kclient audio using pulse server %s", server)
	}
	h := &audioHub{
		device:       device,
		server:       server,
		micSocket:    micSocket,
		audioEnabled: enabled,
		clients:      make(map[*audioClient]struct{}),
		broadcast:    make(chan []byte, audioBroadcastBuffer),
	}
	go h.fanout()
	return h
}

type audioClient struct {
	hub       *audioHub
	conn      *websocket.Conn
	send      chan []byte
	stop      chan struct{}
	closeOnce sync.Once
	opened    bool
	micErrLog sync.Once
}

type audioSource struct {
	hub      *audioHub
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	stderr   bytes.Buffer
	stopCh   chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

type audioCommand struct {
	Type string `json:"type"`
}

func (h *audioHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("audio upgrade: %v", err)
		return
	}
	defer conn.Close()

	c := &audioClient{
		hub:  h,
		conn: conn,
		send: make(chan []byte, audioClientBuffer),
		stop: make(chan struct{}),
	}
	defer func() {
		h.unsubscribe(c)
		c.shutdown()
	}()
	go c.writeLoop()

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage {
			if err := os.WriteFile(h.micSocket, data, 0o644); err != nil {
				c.micErrLog.Do(func() {
					log.Printf("write mic data to %s: %v", h.micSocket, err)
				})
			}
			continue
		}

		var cmd audioCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			log.Printf("audio: invalid message from %s: %v", r.RemoteAddr, err)
			continue
		}
		switch cmd.Type {
		case "open":
			c.enableAudio()
		case "close":
			c.disableAudio()
		}
	}
}

func (c *audioClient) enableAudio() {
	if !c.hub.audioEnabled || c.opened {
		return
	}
	c.opened = true
	c.hub.subscribe(c)
	c.hub.startSource()
}

func (c *audioClient) disableAudio() {
	if !c.opened {
		return
	}
	c.opened = false
	c.hub.unsubscribe(c)
}

func (c *audioClient) writeLoop() {
	for {
		select {
		case data := <-c.send:
			if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				log.Printf("audio write binary: %v", err)
				c.hub.unsubscribe(c)
				c.shutdown()
				return
			}
		case <-c.stop:
			return
		}
	}
}

func (c *audioClient) shutdown() {
	c.closeOnce.Do(func() {
		close(c.stop)
		_ = c.conn.Close()
	})
}

func (h *audioHub) subscribe(c *audioClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *audioHub) unsubscribe(c *audioClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c)
	stopSource := len(h.clients) == 0
	h.mu.Unlock()

	if stopSource {
		h.stopSource()
	}
}

func (h *audioHub) startSource() {
	if !h.audioEnabled {
		return
	}
	h.sourceMu.Lock()
	defer h.sourceMu.Unlock()
	if h.source != nil {
		return
	}
	src, err := h.newAudioSource()
	if err != nil {
		log.Printf("audio: start parec: %v", err)
		return
	}
	h.source = src
	go src.run()
}

func (h *audioHub) stopSource() {
	h.sourceMu.Lock()
	src := h.source
	h.source = nil
	h.sourceMu.Unlock()
	if src != nil {
		src.stop()
	}
}

func (h *audioHub) sourceFinished(src *audioSource) {
	h.sourceMu.Lock()
	if h.source == src {
		h.source = nil
	}
	h.sourceMu.Unlock()
}

func (h *audioHub) newAudioSource() (*audioSource, error) {
	cmd := exec.Command("parec",
		"--device="+h.device,
		"--format=s16le",
		"--rate=44100",
		"--channels=2",
	)
	cmd.Env = os.Environ()
	if h.server != "" {
		cmd.Env = append(cmd.Env, "PULSE_SERVER="+pulseServerEnv(h.server))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	src := &audioSource{
		hub:    h,
		cmd:    cmd,
		stdout: stdout,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	cmd.Stderr = &src.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return src, nil
}

func (s *audioSource) run() {
	defer func() {
		if s.cmd != nil {
			_ = s.cmd.Wait()
		}
		if s.stderr.Len() > 0 {
			log.Printf("audio: parec stderr: %s", s.stderr.String())
		}
		s.hub.sourceFinished(s)
		close(s.done)
	}()

	buf := make([]byte, audioChunkSize)
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		n, err := s.stdout.Read(buf)
		if n > 0 && !allZero(buf[:n]) {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case s.hub.broadcast <- chunk:
			case <-s.stopCh:
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("audio: read parec: %v", err)
			}
			return
		}
	}
}

func (s *audioSource) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
	<-s.done
}

func (h *audioHub) fanout() {
	for chunk := range h.broadcast {
		h.broadcastChunk(chunk)
	}
}

func (h *audioHub) broadcastChunk(chunk []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- chunk:
		case <-c.stop:
		default:
			// Drop the oldest-arriving chunk for a slow client instead of
			// blocking the shared capture goroutine.
		}
	}
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

// detectPulseServer finds the per-user PulseAudio socket when the caller did
// not configure one explicitly.
func detectPulseServer() string {
	if server := os.Getenv("PULSE_SERVER"); server != "" {
		return server
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		socketPath := filepath.Join(runtimeDir, "pulse", "native")
		if info, err := os.Stat(socketPath); err == nil && !info.IsDir() {
			return socketPath
		}
	}
	return ""
}

func pulseServerEnv(server string) string {
	if strings.HasPrefix(server, "unix:") ||
		strings.HasPrefix(server, "tcp:") ||
		strings.HasPrefix(server, "udp:") {
		return server
	}
	if filepath.IsAbs(server) || strings.HasPrefix(server, "/") {
		return "unix:" + server
	}
	return server
}
