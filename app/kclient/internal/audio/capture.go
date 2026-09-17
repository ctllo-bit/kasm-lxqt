package audio

/*
capture.go：负责“拿到声音”

PulseAudio
   ↓
kasm_sink.monitor
   ↓
parec
   ↓
PCM 原始音频
*/

import (
	"io"
	"os"
	"os/exec"
)

const (
	SampleRate   = 48000
	Channels     = 2
	FrameSamples = 960

	// 20ms:
	// 960 samples × 2 channels × 2 bytes(S16LE)
	FrameBytes = FrameSamples * Channels * 2
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
		"--rate=48000",
		"--channels=2",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &Capture{
		cmd:    cmd,
		reader: stdout,
	}, nil
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
	}

	return nil
}
