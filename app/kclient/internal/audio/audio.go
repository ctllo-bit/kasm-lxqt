package audio

import (
	"log"
	"net"
	"net/http"
	"os"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

// NewAudioHandler 创建音频 Socket.IO 服务，返回 http.Handler。
// 路径固定为 /audio/socket.io/。
func NewAudioHandler() http.Handler {
	audioOpts := socketio.DefaultServerOptions()
	audioOpts.SetPath("/audio/socket.io/")

	audioIO := socketio.NewServer(nil, audioOpts)
	registerAudioHandlers(audioIO)
	return audioIO.ServeHandler(nil)
}

// registerAudioHandlers 注册音频 socket.io 事件。
func registerAudioHandlers(io *socketio.Server) {
	io.On("connection", func(clients ...any) {
		s, ok := clients[0].(*socketio.Socket)
		if !ok {
			return
		}

		sess := &audioSession{s: s}

		s.On("open", func(...any) { sess.startCapture() })
		s.On("close", func(...any) { sess.stopCapture() })
		s.On("disconnect", func(...any) { sess.stopCapture() })
		s.On("micdata", func(datas ...any) { sess.writeMic(datas...) })
	})
}

// ------------------------------------------------------------
// audioSession：单个 socket 连接的音频状态
// ------------------------------------------------------------

type audioSession struct {
	s       *socketio.Socket
	capture *Capture
}

// startCapture 启动 PulseAudio 采集，并把 PCM 帧推给 socket。
// 幂等：已在采集中直接返回。
func (a *audioSession) startCapture() {
	if a.capture != nil {
		return
	}

	c, err := StartPulseCapture()
	if err != nil {
		log.Printf("[audio] start pulse capture: %v", err)
		return
	}
	a.capture = c

	go a.pumpFrames(c)
}

// stopCapture 停止采集。
// 幂等：未采集时直接返回。
func (a *audioSession) stopCapture() {
	if a.capture == nil {
		return
	}
	_ = a.capture.Close()
	a.capture = nil
}

// pumpFrames 从 capture 读取 PCM 帧，emit "audio" → chunk。
// 读出错（capture 已关闭）或 Emit 失败即退出。
func (a *audioSession) pumpFrames(cap *Capture) {
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

		if err := a.s.Emit("audio", chunk); err != nil {
			return
		}
	}
}

// writeMic 处理上行麦克风 PCM。
func (a *audioSession) writeMic(datas ...any) {
	if len(datas) == 0 {
		return
	}
	data, ok := datas[0].([]byte)
	if !ok {
		return
	}
	if err := writeMicSink(data, "/defaults/mic.sock"); err != nil {
		log.Printf("[audio] mic write: %v", err)
	}
}

// 上行：把麦克风 PCM 写到 KasmVNC 的虚拟麦克风端点。
func writeMicSink(data []byte, micSockPath string) error {
	// 先按 FIFO 打开
	f, err := os.OpenFile(micSockPath, os.O_WRONLY, 0)
	if err == nil {
		_, werr := f.Write(data)
		_ = f.Close()
		return werr
	}

	// 退回 Unix domain socket
	conn, derr := net.Dial("unix", micSockPath)
	if derr != nil {
		return err // 返回 FIFO 的原始错误更有信息量
	}
	defer conn.Close()
	_, werr := conn.Write(data)
	return werr
}
