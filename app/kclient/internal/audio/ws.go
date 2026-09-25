package audio

import (
	"encoding/binary"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 生产环境需校验
}

type WSSession struct {
	conn      *websocket.Conn
	capture   *Capture
	encoder   *OpusEncoder
	stop      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex
}

func HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	capture, err := StartPulseCapture()
	if err != nil {
		conn.Close()
		return
	}

	encoder, err := StartOpusEncoder()
	if err != nil {
		capture.Close()
		conn.Close()
		return
	}

	s := &WSSession{
		conn:    conn,
		capture: capture,
		encoder: encoder,
		stop:    make(chan struct{}),
	}

	go s.streamAudio()

	// 阻塞读，用于检测客户端断开
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	s.Close()
}

func (s *WSSession) streamAudio() {
	pcm := make([]byte, FrameBytes)
	var seq uint32
	start := time.Now()

	for {
		select {
		case <-s.stop:
			return
		default:
		}

		if _, err := s.capture.Read(pcm); err != nil {
			if !isClosed(s.stop) {
				log.Printf("ws capture: %v", err)
			}
			s.Close()
			return
		}

		opusPacket, err := s.encoder.Encode(pcm)
		if err != nil {
			log.Printf("ws opus encode: %v", err)
			s.Close()
			return
		}

		// 包头：seq(4) + timestamp_ms(8) + opus payload
		buf := make([]byte, 12+len(opusPacket))
		binary.BigEndian.PutUint32(buf[0:4], seq)
		binary.BigEndian.PutUint64(buf[4:12], uint64(time.Since(start).Milliseconds()))
		copy(buf[12:], opusPacket)
		seq++

		s.writeMu.Lock()
		err = s.conn.WriteMessage(websocket.BinaryMessage, buf)
		s.writeMu.Unlock()

		if err != nil {
			if !isClosed(s.stop) {
				log.Printf("ws write: %v", err)
			}
			s.Close()
			return
		}
	}
}

func (s *WSSession) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		if s.capture != nil {
			s.capture.Close()
		}
		if s.encoder != nil {
			s.encoder.Close()
		}
		if s.conn != nil {
			s.conn.Close()
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
