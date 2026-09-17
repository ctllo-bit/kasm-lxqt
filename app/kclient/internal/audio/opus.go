package audio

/*
opus.go：负责“把声音压缩成 WebRTC 能传的格式”

PCM 很大，不适合直接通过 WebRTC 发送，所以要编码：
		PCM
		↓
		libopus
		↓
		Opus packet
*/

/*
#cgo LDFLAGS: -lopus
#include <opus/opus.h>
#include <stdlib.h>

static OpusEncoder* create_encoder(int sample_rate, int channels, int application, int* err) {
    return opus_encoder_create(sample_rate, channels, application, err);
}

static void destroy_encoder(OpusEncoder* enc) {
    opus_encoder_destroy(enc);
}

static int encode_frame(
    OpusEncoder* enc,
    const opus_int16* pcm,
    int frame_size,
    unsigned char* output,
    int max_data_bytes,
    int fec
) {
    opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(fec));
    return opus_encode(
        enc,
        pcm,
        frame_size,
        output,
        max_data_bytes
    );
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type OpusEncoder struct {
	enc *C.OpusEncoder
}

func StartOpusEncoder() (*OpusEncoder, error) {
	var ret C.int

	enc := C.create_encoder(
		C.int(SampleRate),
		C.int(Channels),
		C.OPUS_APPLICATION_AUDIO,
		&ret,
	)

	if enc == nil || ret != C.OPUS_OK {
		return nil, fmt.Errorf("opus_encoder_create failed: %d", int(ret))
	}

	return &OpusEncoder{
		enc: enc,
	}, nil
}

// Encode 把一帧 20ms 的 S16LE PCM 编码成一个 Opus packet。
func (e *OpusEncoder) Encode(pcm []byte) ([]byte, error) {
	if len(pcm) != FrameBytes {
		return nil, fmt.Errorf(
			"invalid PCM size: got %d, want %d",
			len(pcm),
			FrameBytes,
		)
	}

	// 最大 Opus packet 大小。
	out := make([]byte, 4000)

	pcmPtr := (*C.opus_int16)(unsafe.Pointer(&pcm[0]))
	outPtr := (*C.uchar)(unsafe.Pointer(&out[0]))

	n := C.encode_frame(
		e.enc,
		pcmPtr,
		C.int(FrameSamples),
		outPtr,
		C.int(len(out)),
		0,
	)

	if n < 0 {
		return nil, fmt.Errorf("opus_encode failed: %d", int(n))
	}

	return out[:int(n)], nil
}

func (e *OpusEncoder) Close() {
	if e.enc != nil {
		C.destroy_encoder(e.enc)
		e.enc = nil
	}
}
