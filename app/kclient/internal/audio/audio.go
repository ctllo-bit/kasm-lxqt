package audio

import (
	"log"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

// Register 把音频命名空间的 connection handler 注册到 io。
func Register(io *socketio.Server) {
	io.On("connection", func(clients ...any) {
		s, ok := clients[0].(*socketio.Socket)
		if !ok {
			return
		}

		var capture *Capture

		startCapture := func() {
			if capture != nil {
				return
			}
			c, err := StartPulseCapture()
			if err != nil {
				log.Printf("[audio] start pulse capture: %v", err)
				return
			}
			capture = c

			go func(cap *Capture) {
				buf := make([]byte, FrameBytes)
				for {
					n, err := cap.Read(buf)
					if err != nil {
						// capture 已关闭，正常退出
						return
					}
					if n == 0 || allZero(buf[:n]) {
						continue
					}
					chunk := make([]byte, n)
					copy(chunk, buf[:n])

					// 直接给这个 socket 发，不再走 Room
					if err := s.Emit("audio", chunk); err != nil {
						return
					}
				}
			}(c)
		}

		stopCapture := func() {
			if capture != nil {
				_ = capture.Close()
				capture = nil
			}
		}

		s.On("open", func(...any) {
			startCapture()
		})
		s.On("close", func(...any) {
			stopCapture()
		})
		s.On("disconnect", func(...any) {
			stopCapture()
		})

		// 上行：麦克风 PCM
		s.On("micdata", func(datas ...any) {
			if len(datas) == 0 {
				return
			}
			data, ok := datas[0].([]byte)
			if !ok {
				return
			}
			if err := writeMicSink(data); err != nil {
				log.Printf("[audio] mic write: %v", err)
			}
		})
	})
}
