package main

import (
	"bufio"
	"crypto/tls"
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
// VNC server. It forwards client-provided headers (Authorization, Cookie,
// Origin, Host) unchanged so that KasmVNC handles authentication natively.
type vncProxy struct {
	target string
}

func newVNCProxy(target string) *vncProxy {
	return &vncProxy{target: target}
}

func (p *vncProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log key request attributes for debugging without printing secrets.
	auth := r.Header.Get("Authorization")
	authPresent := "false"
	if auth != "" {
		authPresent = "true"
	}
	// Extract cookie names but not values.
	cookieNames := []string{}
	for _, c := range r.Cookies() {
		cookieNames = append(cookieNames, c.Name)
	}
	log.Printf("vncProxy ServeHTTP path=%s host=%s upgrade=%v auth=%s origin=%q cookies=%v remote=%s",
		r.URL.Path, r.Host, isWebSocketUpgrade(r), authPresent, r.Header.Get("Origin"), cookieNames, r.RemoteAddr)
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

	outbound := buildUpstreamRequest(r, target)
	if err := outbound.Write(upstream); err != nil {
		log.Printf("vnc proxy write request: %v", err)
		writeBadGateway(client)
		return
	}

	if err := relayHandshake(client, upstream); err != nil {
		log.Printf("vnc proxy relay handshake: %v", err)
	}
}

func buildUpstreamRequest(r *http.Request, target *url.URL) *http.Request {
	outbound := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		},
		// Preserve the original client Host so the upstream sees the same
		// Host header the client sent (this allows websockify/KasmVNC to
		// validate Host/Origin as if the client connected directly).
		Host:   r.Host,
		Header: r.Header.Clone(),
	}
	// Do not forward Proxy-Connection header. Do NOT strip Authorization;
	// client-provided auth (Cookie/Token/Authorization) must be forwarded
	// unchanged so KasmVNC native authentication works.
	outbound.Header.Del("Proxy-Connection")
	// Make sure the Host header matches the upstream target so TLS/SNI and
	// virtual-hosting checks behave as expected.
	// Do not overwrite the Host header; let the client's Host be forwarded.
	return outbound
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
	code, ok := parseStatusCode(statusLine)
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
	// Forward the complete status line and headers to the client.
	if _, err := client.Write([]byte(statusLine + headers.String())); err != nil {
		return err
	}
	if !ok || code != http.StatusSwitchingProtocols {
		return fmt.Errorf("upstream returned %q", strings.TrimSpace(statusLine))
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
