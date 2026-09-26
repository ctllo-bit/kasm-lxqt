package audio

import (
	"net"
	"os"
)

const micSockPath = "/defaults/mic.sock"

// 上行：把麦克风 PCM 写到 KasmVNC 的虚拟麦克风端点。
// 原版 Node 用 fs.writeFile，说明它其实是一个 FIFO。
func writeMicSink(data []byte) error {
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
