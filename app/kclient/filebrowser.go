package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type filesHub struct {
	root          string
	maxUploadSize int64
}

type fbCommand struct {
	Type      string `json:"type"`
	Directory string `json:"directory"`
	Path      string `json:"path"`
	File      string `json:"file"`
	Item      string `json:"item"`
	Dir       string `json:"dir"`
	Render    bool   `json:"render"`
}

type renderFilesMsg struct {
	Type      string   `json:"type"`
	Root      string   `json:"root"`
	Dirs      []string `json:"dirs"`
	Files     []string `json:"files"`
	Directory string   `json:"directory"`
}

type downloadMsg struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type uploadAckMsg struct {
	Type string `json:"type"`
}

type errorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type uploadTarget struct {
	path      string
	directory string
	render    bool
}

type fbConn struct {
	conn    *websocket.Conn
	hub     *filesHub
	writeMu sync.Mutex
}

func (h *filesHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("filebrowser upgrade: %v", err)
		return
	}
	defer conn.Close()

	c := &fbConn{conn: conn, hub: h}
	var pending *uploadTarget

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage {
			if pending == nil {
				log.Printf("filebrowser: unexpected binary message from %s", r.RemoteAddr)
				continue
			}
			c.handleUploadData(*pending, data)
			pending = nil
			continue
		}

		var cmd fbCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			c.sendError("invalid message: " + err.Error())
			continue
		}

		switch cmd.Type {
		case "open":
			c.renderDirectory(h.root)
		case "getfiles":
			c.renderDirectory(cmd.Directory)
		case "download":
			c.downloadFile(cmd.File)
		case "upload":
			target, err := c.hub.resolve(cmd.Path)
			if err != nil {
				c.sendError(err.Error())
				continue
			}
			if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
				c.sendError("cannot upload over an existing directory")
				continue
			}
			pending = &uploadTarget{path: target, directory: cmd.Directory, render: cmd.Render}
		case "delete":
			c.deleteFiles(cmd.Item, cmd.Directory)
		case "createfolder":
			c.createFolder(cmd.Dir, cmd.Directory)
		default:
			c.sendError("unknown command: " + cmd.Type)
		}
	}
}

func (c *fbConn) renderDirectory(directory string) {
	abs, err := c.hub.resolve(directory)
	if err != nil {
		c.sendError(err.Error())
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		c.sendError("read directory: " + err.Error())
		return
	}

	msg := renderFilesMsg{
		Type:      "renderfiles",
		Root:      filepath.ToSlash(c.hub.root),
		Directory: filepath.ToSlash(abs),
		Dirs:      []string{},
		Files:     []string{},
	}
	for _, entry := range entries {
		if entry.IsDir() {
			msg.Dirs = append(msg.Dirs, entry.Name())
		} else {
			msg.Files = append(msg.Files, entry.Name())
		}
	}
	c.writeJSON(msg)
}

func (c *fbConn) downloadFile(file string) {
	abs, err := c.hub.resolve(file)
	if err != nil {
		c.sendError(err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		c.sendError("stat file: " + err.Error())
		return
	}
	if info.IsDir() {
		c.sendError("cannot download a directory")
		return
	}
	if info.Size() > c.hub.maxUploadSize {
		c.sendError(fmt.Sprintf("file too large: %d bytes (limit %d)", info.Size(), c.hub.maxUploadSize))
		return
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		c.sendError("read file: " + err.Error())
		return
	}
	c.writeJSON(downloadMsg{Type: "download", Name: filepath.Base(abs)})
	c.writeBinary(data)
}

func (c *fbConn) handleUploadData(target uploadTarget, data []byte) {
	if int64(len(data)) > c.hub.maxUploadSize {
		c.sendError(fmt.Sprintf("file too large: %d bytes (limit %d)", len(data), c.hub.maxUploadSize))
		return
	}
	if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
		c.sendError("create parent directory: " + err.Error())
		return
	}
	if err := os.WriteFile(target.path, data, 0o644); err != nil {
		c.sendError("write file: " + err.Error())
		return
	}
	c.writeJSON(uploadAckMsg{Type: "upload-ack"})
	if target.render {
		c.renderDirectory(target.directory)
	}
}

func (c *fbConn) deleteFiles(item, directory string) {
	abs, err := c.hub.resolve(item)
	if err != nil {
		c.sendError(err.Error())
		return
	}
	info, err := os.Lstat(abs)
	if err != nil {
		c.sendError("stat item: " + err.Error())
		return
	}
	if info.IsDir() {
		err = os.RemoveAll(abs)
	} else {
		err = os.Remove(abs)
	}
	if err != nil {
		c.sendError("delete item: " + err.Error())
		return
	}
	c.renderDirectory(directory)
}

func (c *fbConn) createFolder(dir, directory string) {
	abs, err := c.hub.resolve(dir)
	if err != nil {
		c.sendError(err.Error())
		return
	}
	if info, statErr := os.Stat(abs); statErr == nil {
		if !info.IsDir() {
			c.sendError("target exists and is not a directory")
			return
		}
	} else if os.IsNotExist(statErr) {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			c.sendError("create directory: " + err.Error())
			return
		}
	} else {
		c.sendError("stat target: " + statErr.Error())
		return
	}
	c.renderDirectory(directory)
}

func (c *fbConn) writeJSON(msg any) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteJSON(msg); err != nil {
		log.Printf("filebrowser write json: %v", err)
	}
}

func (c *fbConn) writeBinary(data []byte) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("filebrowser write binary: %v", err)
	}
}

func (c *fbConn) sendError(message string) {
	c.writeJSON(errorMsg{Type: "error", Message: message})
}

// resolve converts a slash-separated client path into an absolute path
// guaranteed to live below the configured file browser root. Symlinks are
// resolved on the nearest existing ancestor so links inside the root cannot
// be used to escape it.
func (h *filesHub) resolve(path string) (string, error) {
	if path == "" {
		path = h.root
	}

	clean := filepath.Clean(filepath.FromSlash(path))
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(h.root, clean)
	}

	resolved, err := resolveExistingAncestors(clean)
	if err != nil {
		return "", err
	}
	if err := ensureWithinRoot(h.root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveExistingAncestors(path string) (string, error) {
	current := path
	var tail []string
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return filepath.Clean(real), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("path does not exist: " + path)
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}

func ensureWithinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("path escapes file browser root")
	}
	return nil
}
