package router

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

// NewFilesHandler 创建文件浏览器 Socket.IO 服务，返回 http.Handler
func NewFilesHandler(fmHome string) http.Handler {
	filesOpts := socketio.DefaultServerOptions()
	filesOpts.SetPath("/files/socket.io/")

	filesIO := socketio.NewServer(nil, filesOpts)
	registerFileHandlers(filesIO, fmHome)
	return filesIO.ServeHandler(nil)
}

// registerFileHandlers 注册文件浏览器 socket.io 事件。
func registerFileHandlers(io *socketio.Server, fmHome string) {
	io.On("connection", func(clients ...any) {
		s, ok := clients[0].(*socketio.Socket)
		if !ok {
			return
		}

		s.On("open", func(...any) { listFiles(s, fmHome) })
		s.On("getfiles", func(datas ...any) { onGetfiles(s, fmHome, datas...) })
		s.On("downloadfile", func(datas ...any) { onDownloadfile(s, datas...) }) //点文件名，浏览器下载
		s.On("deletefiles", func(datas ...any) { onDeletefiles(s, datas...) })
		s.On("createfolder", func(datas ...any) { onCreatefolder(s, datas...) })
	})
}

// listFiles 读目录，emit "renderfiles" → [dirs, files, directory]。
func listFiles(s *socketio.Socket, directory string) {
	if directory == "" {
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		log.Printf("[files] readdir %s: %v", directory, err)
		s.Emit("renderfiles", []any{[]string{}, []string{}, directory})
		return
	}

	dirs, files := []string{}, []string{}
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else {
			files = append(files, e.Name())
		}
	}
	s.Emit("renderfiles", []any{dirs, files, directory})
}

// onGetfiles 切换到指定目录，无参数时回默认目录。
func onGetfiles(s *socketio.Socket, fmHome string, datas ...any) {
	if len(datas) == 0 {
		listFiles(s, fmHome)
		return
	}
	dir, _ := datas[0].(string)
	listFiles(s, dir)
}

// onDownloadfile 读取文件，emit "sendfile" → [data, fileName]。
func onDownloadfile(s *socketio.Socket, datas ...any) {
	if len(datas) == 0 {
		return
	}
	file, _ := datas[0].(string)
	data, err := os.ReadFile(file)
	if err != nil {
		log.Printf("[files] read %s: %v", file, err)
		return
	}
	s.Emit("sendfile", []any{data, filepath.Base(file)})
}

// filesUploadHandler 处理 POST /files/upload。
// 表单字段：filepath（绝对路径）、file（文件流）。
func filesUploadHandler(fmHome string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 限制总大小 500MB
		r.Body = http.MaxBytesReader(w, r.Body, 500<<20)

		// 32MB 内存缓冲，超出部分自动落临时文件
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
			return
		}

		filePath := r.FormValue("filepath")
		if filePath == "" {
			http.Error(w, "missing filepath", http.StatusBadRequest)
			return
		}

		// 路径必须在 fmHome 下
		abs, _ := filepath.Abs(filePath)
		homeAbs, _ := filepath.Abs(fmHome)
		if !strings.HasPrefix(abs, homeAbs+string(filepath.Separator)) {
			http.Error(w, "path out of home", http.StatusForbidden)
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()

		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dst, err := os.Create(abs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil { // 流式，不占内存
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// onDeletefiles 删除文件或目录，然后刷新列表。
// datas[0] = [item, directory]
func onDeletefiles(s *socketio.Socket, datas ...any) {
	if len(datas) == 0 {
		return
	}
	res, ok := toSlice(datas[0])
	if !ok || len(res) < 2 {
		return
	}

	item, _ := res[0].(string)
	directory, _ := res[1].(string)
	if item == "" {
		return
	}

	info, err := os.Stat(item)
	if err != nil {
		log.Printf("[files] stat %s: %v", item, err)
		listFiles(s, directory)
		return
	}

	if info.IsDir() {
		if err := os.RemoveAll(item); err != nil {
			log.Printf("[files] removeall %s: %v", item, err)
		}
	} else {
		if err := os.Remove(item); err != nil {
			log.Printf("[files] remove %s: %v", item, err)
		}
	}

	listFiles(s, directory)
}

// onCreatefolder 新建文件夹，然后刷新列表。
// datas[0] = [dir, directory]
func onCreatefolder(s *socketio.Socket, datas ...any) {
	if len(datas) == 0 {
		return
	}
	res, ok := toSlice(datas[0])
	if !ok || len(res) < 2 {
		return
	}

	dir, _ := res[0].(string)
	directory, _ := res[1].(string)
	if dir == "" {
		return
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[files] mkdir %s: %v", dir, err)
		}
	}

	listFiles(s, directory)
}

// 工具函数
func toSlice(v any) ([]any, bool) {
	if x, ok := v.([]any); ok {
		return x, true
	}
	log.Printf("[files] toSlice: unexpected type %T", v)
	return nil, false
}
