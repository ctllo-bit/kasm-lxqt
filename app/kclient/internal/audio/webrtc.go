package audio

/*扬声器：
Linux → PulseAudio → Go → Opus → WebRTC → 浏览器

kasm_sink.monitor
        ↓
      parec
        ↓
      PCM
        ↓
     libopus
        ↓
    Opus packet
        ↓
 Pion WebRTC Track
        ↓
       网络
        ↓
     浏览器
        ↓
      <audio>
*/

/*
webrtc.go：负责“把 Opus 发给浏览器”

1、创建一个：音频轨道并告诉 WebRTC。
2、把每个 Opus packet 持续塞进 WebRTC。
*/

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type WebRTCSession struct {
	pc        *webrtc.PeerConnection
	track     *webrtc.TrackLocalStaticSample
	capture   *Capture
	encoder   *OpusEncoder
	stop      chan struct{}
	closeOnce sync.Once
}

func NewWebRTCSession() (*WebRTCSession, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio",
		"kasm-audio",
	)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return nil, err
	}

	capture, err := StartPulseCapture()
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	encoder, err := StartOpusEncoder()
	if err != nil {
		capture.Close()
		_ = pc.Close()
		return nil, err
	}

	session := &WebRTCSession{
		pc:      pc,
		track:   track,
		capture: capture,
		encoder: encoder,
		stop:    make(chan struct{}),
	}

	go session.streamAudio()

	return session, nil
}

func (s *WebRTCSession) streamAudio() {
	pcm := make([]byte, FrameBytes)

	for {
		select {
		case <-s.stop:
			return
		default:
		}

		if _, err := s.capture.Read(pcm); err != nil {
			if !isClosed(s.stop) {
				log.Printf("webrtc audio capture: %v", err)
			}
			return
		}

		opusPacket, err := s.encoder.Encode(pcm)
		if err != nil {
			log.Printf("webrtc opus encode: %v", err)
			return
		}

		err = s.track.WriteSample(media.Sample{
			Data:     opusPacket,
			Duration: 20 * time.Millisecond,
		})
		if err != nil {
			if !isClosed(s.stop) {
				log.Printf("webrtc audio write: %v", err)
			}
			return
		}
	}
}

func (s *WebRTCSession) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)

		if s.capture != nil {
			_ = s.capture.Close()
		}

		if s.encoder != nil {
			s.encoder.Close()
		}

		if s.pc != nil {
			_ = s.pc.Close()
		}
	})
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// HandleOffer:
// 浏览器 POST SDP Offer
// Go 返回 SDP Answer
func HandleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	offerSDP, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(offerSDP) == 0 {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		return
	}

	session, err := NewWebRTCSession()
	if err != nil {
		http.Error(w, fmt.Sprintf("create WebRTC session: %v", err), http.StatusInternalServerError)
		return
	}

	// 浏览器断开时关闭 session。
	session.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("WebRTC connection state: %s", state)

		switch state {
		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			session.Close()
		}
	})

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(offerSDP),
	}

	if err := session.pc.SetRemoteDescription(offer); err != nil {
		session.Close()
		http.Error(w, fmt.Sprintf("set remote description: %v", err), http.StatusBadRequest)
		return
	}

	answer, err := session.pc.CreateAnswer(nil)
	if err != nil {
		session.Close()
		http.Error(w, fmt.Sprintf("create answer: %v", err), http.StatusInternalServerError)
		return
	}

	if err := session.pc.SetLocalDescription(answer); err != nil {
		session.Close()
		http.Error(w, fmt.Sprintf("set local description: %v", err), http.StatusInternalServerError)
		return
	}

	// 等 ICE candidates 收集完成。
	<-webrtc.GatheringCompletePromise(session.pc)

	local := session.pc.LocalDescription()
	if local == nil {
		session.Close()
		http.Error(w, "local description is nil", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/sdp")
	_, _ = w.Write([]byte(local.SDP))
}
