package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const audioChunkSize = 4096

type audioHub struct {
	device       string
	server       string
	micSocket    string
	audioEnabled bool
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
	return &audioHub{
		device:       device,
		server:       server,
		micSocket:    micSocket,
		audioEnabled: enabled,
	}
}

type audioConn struct {
	hub       *audioHub
	conn      *websocket.Conn
	writeMu   sync.Mutex
	cmdMu     sync.Mutex
	cmd       *exec.Cmd
	stop      chan struct{}
	done      chan struct{}
	micErrLog sync.Once
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

	a := &audioConn{hub: h, conn: conn}
	defer a.stopRecorder()

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage {
			if err := os.WriteFile(h.micSocket, data, 0o644); err != nil {
				a.micErrLog.Do(func() {
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
			a.startRecorder()
		case "close":
			a.stopRecorder()
		}
	}
}

func (a *audioConn) startRecorder() {
	if !a.hub.audioEnabled {
		return
	}

	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()
	// 已经有 recorder 在运行
	if a.cmd != nil {
		return
	}

	cmd := exec.Command("parec",
		"--device="+a.hub.device,
		"--format=s16le",
		"--rate=44100",
		"--channels=2",
	)
	cmd.Env = os.Environ()
	if a.hub.server != "" {
		cmd.Env = append(
			cmd.Env,
			"PULSE_SERVER="+pulseServerEnv(a.hub.server),
		)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("audio: create stdout pipe: %v", err)
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		log.Printf("audio: start parec: %v", err)
		return
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	a.cmd = cmd
	a.stop = stop
	a.done = done

	go func() {
		defer close(done)
		defer func() {
			_ = cmd.Wait()
			a.cmdMu.Lock()
			if a.cmd == cmd {
				a.cmd = nil
				a.stop = nil
				a.done = nil
			}
			a.cmdMu.Unlock()
			if stderr.Len() > 0 {
				log.Printf("audio: parec stderr: %s", stderr.String())
			}
		}()

		buf := make([]byte, audioChunkSize)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 && !allZero(buf[:n]) {
				a.writeBinary(buf[:n])
			}
			if readErr != nil {
				return
			}
			select {
			case <-stop:
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			default:
			}
		}
	}()
}

func (a *audioConn) stopRecorder() {
	a.cmdMu.Lock()
	cmd := a.cmd
	stop := a.stop
	done := a.done

	a.cmd = nil
	a.stop = nil
	a.done = nil
	a.cmdMu.Unlock()
	if stop != nil {
		close(stop)
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if done != nil {
		<-done
	}
}

func (a *audioConn) writeBinary(data []byte) {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if err := a.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("audio write binary: %v", err)
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
