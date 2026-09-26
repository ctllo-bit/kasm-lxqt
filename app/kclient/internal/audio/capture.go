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

func StartPulseCapture() (*Capture, error) {
	cmd := exec.Command(
		"parec",
		"--server=unix:/run/remote-desktop/pulse/native",
		"--device=kasm_sink.monitor",
		"--format=s16le",
		"--rate=44100",
		"--channels=2",
		"--raw",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Capture{cmd: cmd, reader: stdout}, nil
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
