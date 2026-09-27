package router

import (
	"log"
	"os"
	"path/filepath"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

// registerFileHandlers 注册文件浏览器 socket.io 事件。
func registerFileHandlers(io *socketio.Server, fmHome string) {
	io.On("connection", func(clients ...any) {
		s, ok := clients[0].(*socketio.Socket)
		if !ok {
			return
		}

		send := func(event string, data any) {
			if err := s.Emit(event, data); err != nil {
				log.Printf("[files] emit %s: %v", event, err)
			}
		}

		// ---------- 列目录 ----------
		listFiles := func(directory string) {
			if directory == "" {
				directory = fmHome
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				log.Printf("[files] readdir %s: %v", directory, err)
				send("renderfiles", []any{[]string{}, []string{}, directory})
				return
			}

			dirs := []string{}
			files := []string{}
			for _, e := range entries {
				if e.IsDir() {
					dirs = append(dirs, e.Name())
				} else {
					files = append(files, e.Name())
				}
			}

			log.Printf("[files] renderfiles dirs=%d files=%d dir=%s",
				len(dirs), len(files), directory)
			send("renderfiles", []any{dirs, files, directory})
		}

		// ---------- 下载文件 ----------
		downloadFile := func(file string) {
			data, err := os.ReadFile(file)
			if err != nil {
				log.Printf("[files] read %s: %v", file, err)
				return
			}
			fileName := filepath.Base(file)
			send("sendfile", []any{data, fileName})
		}

		// ---------- 上传文件 ----------
		// res[0] 当前目录（渲染用）
		// res[1] 完整文件路径
		// res[2] 文件数据
		// res[3] 是否最后一帧（true 时刷新列表）
		uploadFile := func(res []any) {
			if len(res) < 4 {
				log.Printf("[files] uploadfile: bad args %d", len(res))
				return
			}
			directory, _ := res[0].(string)
			filePath, _ := res[1].(string)
			data := toBytes(res[2])
			render, _ := res[3].(bool)

			if filePath == "" || data == nil {
				log.Printf("[files] uploadfile: path=%q dataType=%T", filePath, res[2])
				return
			}

			folder := filepath.Dir(filePath)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				log.Printf("[files] mkdir %s: %v", folder, err)
				return
			}
			if err := os.WriteFile(filePath, data, 0o644); err != nil {
				log.Printf("[files] write %s: %v", filePath, err)
				return
			}

			if render {
				listFiles(directory)
			}
		}

		// ---------- 删除 ----------
		deleteFiles := func(res []any) {
			if len(res) < 2 {
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
				listFiles(directory)
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
			listFiles(directory)
		}

		// ---------- 新建文件夹 ----------
		createFolder := func(res []any) {
			if len(res) < 2 {
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
			listFiles(directory)
		}

		// ---------- 事件绑定 ----------
		s.On("open", func(...any) {
			log.Printf("[files] open")
			listFiles(fmHome)
		})

		s.On("getfiles", func(datas ...any) {
			if len(datas) == 0 {
				listFiles(fmHome)
				return
			}
			dir, _ := datas[0].(string)
			listFiles(dir)
		})

		s.On("downloadfile", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			file, _ := datas[0].(string)
			downloadFile(file)
		})

		s.On("uploadfile", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			log.Printf("[files] uploadfile argType=%T", datas[0])
			res, ok := toSlice(datas[0])
			if !ok {
				return
			}
			uploadFile(res)
		})

		s.On("deletefiles", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			res, ok := toSlice(datas[0])
			if !ok {
				return
			}
			deleteFiles(res)
		})

		s.On("createfolder", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			res, ok := toSlice(datas[0])
			if !ok {
				return
			}
			createFolder(res)
		})
	})
}

// toSlice 把前端传来的数组参数统一成 []any。
func toSlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	}
	log.Printf("[files] toSlice: unexpected type %T", v)
	return nil, false
}

// toBytes 把前端传来的二进制数据转成 []byte。
func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	case []any:
		b := make([]byte, len(x))
		for i, n := range x {
			if f, ok := n.(float64); ok {
				b[i] = byte(f)
			}
		}
		return b
	}
	if b, ok := v.(interface{ Bytes() []byte }); ok {
		return b.Bytes()
	}
	return nil
}
