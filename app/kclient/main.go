package main

import (
	"crypto/tls"
	"encoding/base64"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	socketio "github.com/googollee/go-socket.io"
)

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

var (
	customUser = getEnv("CUSTOM_USER", "abc")
	password   = getEnv("PASSWORD", "123456")
	subfolder  = getEnv("SUBFOLDER", "/")
	title      = getEnv("TITLE", "KasmVNC Client")
	fmHome     = getEnv("FM_HOME", "/config")
)

// 解析并替换前端 EJS 模板标签，彻底解决 URIError: URI malformed 问题
func renderEjsFile(w http.ResponseWriter, filePath string, titleVal string, wsPath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	str := string(content)
	str = strings.ReplaceAll(str, "<%= title %>", titleVal)
	str = strings.ReplaceAll(str, "<%- title %>", titleVal)

	// 替换 <% if (path) { %>...<% } %> 结构
	re := regexp.MustCompile(`(?s)<%\s*if\s*\(\s*path\s*\)\s*\{\s*%>(.*?)<%\s*\}\s*%>`)
	if wsPath != "" {
		str = re.ReplaceAllStringFunc(str, func(m string) string {
			sub := re.FindStringSubmatch(m)
			if len(sub) > 1 {
				res := strings.ReplaceAll(sub[1], "<%= path %>", wsPath)
				res = strings.ReplaceAll(res, "<%- path %>", wsPath)
				return res
			}
			return wsPath
		})
	} else {
		str = re.ReplaceAllString(str, "")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(str))
}

// 专门处理 socket.io.js 客户端脚本请求
func handleSocketIOJS(w http.ResponseWriter, r *http.Request) {
	localPaths := []string{
		"./node_modules/socket.io/client-dist/socket.io.js",
		"./node_modules/socket.io-client/dist/socket.io.js",
	}
	for _, p := range localPaths {
		if _, err := os.Stat(p); err == nil {
			http.ServeFile(w, r, p)
			return
		}
	}
	// 本地不存在 node_modules 时重定向至 CDN (Socket.IO v2 兼容版本)
	http.Redirect(w, r, "https://cdnjs.cloudflare.com/ajax/libs/socket.io/2.3.0/socket.io.js", http.StatusFound)
}

func main() {
	if !strings.HasSuffix(subfolder, "/") {
		subfolder += "/"
	}

	wsPath := ""
	if subfolder != "/" {
		wsPath = "&path=" + strings.TrimPrefix(subfolder, "/")[0:len(strings.TrimPrefix(subfolder, "/"))-1] + "/websockify"
	}

	mux := http.NewServeMux()

	// ---- 1. Static Files & Handlers ----
	mux.Handle(subfolder+"public/", http.StripPrefix(subfolder+"public/", http.FileServer(http.Dir("public"))))
	mux.Handle(subfolder+"vnc/", http.StripPrefix(subfolder+"vnc/", http.FileServer(http.Dir("/usr/share/kasmvnc/www/"))))

	mux.HandleFunc(subfolder, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == subfolder || r.URL.Path == subfolder+"index.html" {
			renderEjsFile(w, "public/index.html", title, wsPath)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc(subfolder+"favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "public/favicon.ico")
	})
	mux.HandleFunc(subfolder+"manifest.json", func(w http.ResponseWriter, r *http.Request) {
		renderEjsFile(w, "public/manifest.json", title, wsPath)
	})
	mux.HandleFunc(subfolder+"files", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "public/filebrowser.html")
	})

	// ---- 2. WebSocket Proxy (Websockify) ----
	targetURL, _ := url.Parse("https://127.0.0.1:8088")
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		auth := customUser + ":" + password
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
		req.Host = targetURL.Host
	}

	mux.HandleFunc(subfolder+"websockify", func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})

	// ---- 3. File Manager Socket.IO ----
	fileServer := socketio.NewServer(nil)
	fileServer.OnConnect("/", func(s socketio.Conn) error {
		return nil
	})
	fileServer.OnEvent("/", "open", func(s socketio.Conn, msg string) {
		getFiles(s, fmHome)
	})
	fileServer.OnEvent("/", "getfiles", func(s socketio.Conn, dir string) {
		getFiles(s, dir)
	})
	fileServer.OnEvent("/", "downloadfile", func(s socketio.Conn, file string) {
		data, err := os.ReadFile(file)
		if err == nil {
			s.Emit("sendfile", []interface{}{data, filepath.Base(file)})
		}
	})
	fileServer.OnEvent("/", "uploadfile", func(s socketio.Conn, req []interface{}) {
		if len(req) >= 4 {
			dir, _ := req[0].(string)
			filePath, _ := req[1].(string)
			dataStr, _ := req[2].(string)
			shouldRender, _ := req[3].(bool)

			os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
			os.WriteFile(filePath, []byte(dataStr), 0644)
			if shouldRender {
				getFiles(s, dir)
			}
		}
	})
	fileServer.OnEvent("/", "deletefiles", func(s socketio.Conn, req []interface{}) {
		if len(req) >= 2 {
			item, _ := req[0].(string)
			dir, _ := req[1].(string)
			item = strings.ReplaceAll(item, "|", "'")
			os.RemoveAll(item)
			getFiles(s, dir)
		}
	})
	fileServer.OnEvent("/", "createfolder", func(s socketio.Conn, req []interface{}) {
		if len(req) >= 2 {
			dirName, _ := req[0].(string)
			baseDir, _ := req[1].(string)
			os.MkdirAll(dirName, os.ModePerm)
			getFiles(s, baseDir)
		}
	})

	go fileServer.Serve()
	defer fileServer.Close()

	// 拦截 socket.io.js，并剥离路径前缀给 fileServer
	mux.HandleFunc(subfolder+"files/socket.io/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "socket.io.js") {
			handleSocketIOJS(w, r)
			return
		}
		http.StripPrefix(subfolder+"files", fileServer).ServeHTTP(w, r)
	})

	// ---- 4. Audio Socket.IO (PulseAudio) ----
	audioServer := socketio.NewServer(nil)
	var audioCmd *exec.Cmd
	var audioEnabled = true

	audioServer.OnConnect("/", func(s socketio.Conn) error {
		return nil
	})
	audioServer.OnEvent("/", "open", func(s socketio.Conn) {
		if !audioEnabled {
			return
		}
		if audioCmd != nil && audioCmd.Process != nil {
			audioCmd.Process.Kill()
		}
		audioCmd = exec.Command("parec", "--device=kasm_sink.monitor", "--format=s16le", "--rate=44100", "--channels=2")
		stdout, err := audioCmd.StdoutPipe()
		if err != nil {
			log.Println("Audio capture error:", err)
			return
		}
		audioCmd.Start()

		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stdout.Read(buf)
				if err != nil {
					break
				}
				if !isSilence(buf[:n]) {
					s.Emit("audio", buf[:n])
				}
			}
		}()
	})

	closeAudio := func(s socketio.Conn) {
		if audioCmd != nil && audioCmd.Process != nil {
			audioCmd.Process.Kill()
		}
	}
	audioServer.OnEvent("/", "close", closeAudio)
	audioServer.OnDisconnect("/", func(s socketio.Conn, reason string) {
		closeAudio(s)
	})

	audioServer.OnEvent("/", "micdata", func(s socketio.Conn, buffer []byte) {
		os.WriteFile("/defaults/mic.sock", buffer, 0644)
	})

	go audioServer.Serve()
	defer audioServer.Close()

	// 拦截 socket.io.js，并剥离路径前缀给 audioServer
	mux.HandleFunc(subfolder+"audio/socket.io/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "socket.io.js") {
			handleSocketIOJS(w, r)
			return
		}
		http.StripPrefix(subfolder+"audio", audioServer).ServeHTTP(w, r)
	})

	// ---- Start Server ----
	log.Println("Starting Server on :6900")
	log.Fatal(http.ListenAndServe(":6900", mux))
}

func getFiles(s socketio.Conn, directory string) {
	entries, err := os.ReadDir(directory)
	var dirs []string
	var files []string
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, entry.Name())
			} else {
				files = append(files, entry.Name())
			}
		}
	}
	if dirs == nil {
		dirs = []string{}
	}
	if files == nil {
		files = []string{}
	}
	s.Emit("renderfiles", []interface{}{dirs, files, directory})
}

func isSilence(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}
