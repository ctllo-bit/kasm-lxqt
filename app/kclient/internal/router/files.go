package router

import (
	socketio "github.com/zishang520/socket.io/v2/socket"
)

// registerFileHandlers 注册文件浏览器 socket.io 事件。
// 先 stub，功能后续补。
func registerFileHandlers(io *socketio.Server) {
	io.On("connection", func(clients ...any) {
		s, ok := clients[0].(*socketio.Socket)
		if !ok {
			return
		}

		s.On("open", func(...any) {})
		s.On("getfiles", func(...any) {})
		s.On("downloadfile", func(...any) {})
		s.On("uploadfile", func(...any) {})
		s.On("deletefiles", func(...any) {})
		s.On("createfolder", func(...any) {})
	})
}
