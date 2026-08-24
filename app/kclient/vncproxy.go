package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// vncProxy forwards KasmVNC websockify WebSocket connections to the backend
// VNC server and injects the Basic auth header, matching the old Node proxy.
type vncProxy struct {
	target   string
	user     string
	password string
}

func newVNCProxy(target, user, password string) *vncProxy {
	return &vncProxy{target: target, user: user, password: password}
}

func (p *vncProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		http.NotFound(w, r)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("vnc proxy hijack: %v", err)
		return
	}
	defer client.Close()

	target, err := url.Parse(p.target)
	if err != nil {
		log.Printf("vnc proxy invalid target %q: %v", p.target, err)
		writeBadGateway(client)
		return
	}

	upstream, err := dialUpstream(target)
	if err != nil {
		log.Printf("vnc proxy dial %s: %v", target.Host, err)
		writeBadGateway(client)
		return
	}
	defer upstream.Close()

	outbound := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		},
		Host:   target.Host,
		Header: r.Header.Clone(),
	}
	outbound.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.user+":"+p.password)))
	outbound.Header.Del("Proxy-Connection")
	if err := outbound.Write(upstream); err != nil {
		log.Printf("vnc proxy write request: %v", err)
		writeBadGateway(client)
		return
	}

	if err := relayHandshake(client, upstream); err != nil {
		log.Printf("vnc proxy relay handshake: %v", err)
	}
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func dialUpstream(target *url.URL) (net.Conn, error) {
	address := target.Host
	if target.Port() == "" {
		if target.Scheme == "https" || target.Scheme == "wss" {
			address += ":443"
		} else {
			address += ":80"
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if target.Scheme == "https" || target.Scheme == "wss" {
		return tls.DialWithDialer(dialer, "tcp", address, &tls.Config{InsecureSkipVerify: true})
	}
	return dialer.Dial("tcp", address)
}

// relayHandshake forwards the upstream 101 response to the client, then pumps
// bytes in both directions until either side closes.
func relayHandshake(client, upstream net.Conn) error {
	reader := bufio.NewReader(upstream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if code, ok := parseStatusCode(statusLine); !ok || code != http.StatusSwitchingProtocols {
		_, _ = client.Write([]byte(statusLine))
		return fmt.Errorf("upstream returned %q", strings.TrimSpace(statusLine))
	}

	var headers strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		headers.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	if _, err := client.Write([]byte(statusLine + headers.String())); err != nil {
		return err
	}
	if reader.Buffered() > 0 {
		buffered, _ := reader.Peek(reader.Buffered())
		if _, err := client.Write(buffered); err != nil {
			return err
		}
	}

	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
	}()
	_, err = io.Copy(client, upstream)
	_ = client.Close()
	return err
}

func parseStatusCode(line string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) < 2 {
		return 0, false
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return code, true
}

func writeBadGateway(conn net.Conn) {
	_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
}
