package audio

import (
	"io"
	"os"
	"os/exec"
)

const (
	SampleRate   = 44100 // 与原版前端 PCM 保持一致
	Channels     = 2
	FrameSamples = 1024
	FrameBytes   = FrameSamples * Channels * 2
)

type Capture struct {
	cmd    *exec.Cmd
	reader io.ReadCloser
}

// StartPulseCapture 使用 parec + ffmpeg 管道从 PulseAudio 捕获原始 PCM 数据。
// parec 负责从 PulseAudio 抓取原始数据，ffmpeg 负责将原始数据转换为 s16le 格式。
func StartPulseCapture() (*Capture, error) {
	// 1. 启动 parec，从 PulseAudio 捕获原始数据
	parecCmd := exec.Command(
		"parec",
		"--server=unix:/run/remote-desktop/pulse/native",
		"--device=kasm_sink.monitor",
		"--format=s16le", // 输出格式 s16le
		"--rate=44100",   // 采样率
		"--channels=2",   // 声道数
		"--raw",          // 原始数据
	)

	// 2. 启动 ffmpeg，从标准输入读取 parec 的输出，并输出原始 PCM 到标准输出
	ffmpegCmd := exec.Command(
		"ffmpeg",
		"-f", "s16le", // 输入格式：s16le
		"-ar", "44100", // 输入采样率
		"-ac", "2", // 输入声道数
		"-i", "pipe:0", // 从标准输入读取
		"-f", "s16le", // 输出格式：s16le
		"-acodec", "pcm_s16le", // 输出编码：pcm_s16le
		"-", // 输出到标准输出
	)

	// 3. 将 parec 的标准输出管道连接到 ffmpeg 的标准输入
	parecStdout, err := parecCmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ffmpegCmd.Stdin = parecStdout

	// 4. 获取 ffmpeg 的标准输出管道，供 Go 程序读取
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	// 5. 将 stderr 保留到控制台，便于调试
	ffmpegCmd.Stderr = os.Stderr

	// 6. 启动 ffmpeg 进程
	if err := ffmpegCmd.Start(); err != nil {
		return nil, err
	}

	// 7. 启动 parec 进程
	if err := parecCmd.Start(); err != nil {
		// 如果 parec 启动失败，需要清理已启动的 ffmpeg
		_ = ffmpegCmd.Process.Kill()
		_ = ffmpegCmd.Wait()
		return nil, err
	}

	// 返回 Capture 结构体，其中包含 ffmpeg 的 cmd 和输出管道
	return &Capture{cmd: ffmpegCmd, reader: ffmpegStdout}, nil
}

func (c *Capture) Read(buf []byte) (int, error) {
	return io.ReadFull(c.reader, buf)
}

func (c *Capture) Close() error {
	if c.reader != nil {
		_ = c.reader.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
