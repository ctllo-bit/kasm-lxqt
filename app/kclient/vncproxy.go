package main

import (
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

var errInvalidVNCUpstream = errors.New(
	"invalid KasmVNC upstream",
)

type vncProxy struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func newVNCProxy(
	target string,
) (*vncProxy, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	if targetURL.Host == "" {
		return nil, errInvalidVNCUpstream
	}

	if targetURL.Scheme != "https" {
		return nil, &url.Error{
			Op:  "parse",
			URL: target,
			Err: errors.New(
				"KasmVNC upstream must use https",
			),
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(
		targetURL,
	)

	originalDirector := proxy.Director

	proxy.Director = func(
		r *http.Request,
	) {
		originalDirector(r)

		// 上游 Host 使用 KasmVNC Host。
		r.Host = targetURL.Host

		// ReverseProxy 默认会处理 WebSocket Upgrade。
		// Authorization / Cookie / Origin 等普通 Header
		// 会正常转发。

		log.Printf(
			"KasmVNC OUT: method=%s scheme=%s host=%s path=%s query=%q upgrade=%q connection=%q origin=%q",
			r.Method,
			r.URL.Scheme,
			r.URL.Host,
			r.URL.Path,
			r.URL.RawQuery,
			r.Header.Get("Upgrade"),
			r.Header.Get("Connection"),
			r.Header.Get("Origin"),
		)
	}

	proxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,

		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,

		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		},

		// KasmVNC WebSocket 使用 HTTP/1.1 Upgrade。
		ForceAttemptHTTP2: false,

		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,

		IdleConnTimeout: 90 * time.Second,

		TLSHandshakeTimeout: 10 * time.Second,

		ExpectContinueTimeout: 1 * time.Second,
	}

	proxy.ModifyResponse = func(
		resp *http.Response,
	) error {
		log.Printf(
			"KasmVNC IN: status=%d method=%s path=%s",
			resp.StatusCode,
			resp.Request.Method,
			resp.Request.URL.Path,
		)

		if resp.StatusCode >= 400 {
			log.Printf(
				"KasmVNC ERROR RESPONSE: status=%d path=%s",
				resp.StatusCode,
				resp.Request.URL.Path,
			)
		}

		return nil
	}

	proxy.ErrorHandler = func(
		w http.ResponseWriter,
		r *http.Request,
		err error,
	) {
		log.Printf(
			"KasmVNC PROXY ERROR: method=%s path=%s err=%v",
			r.Method,
			r.URL.Path,
			err,
		)

		http.Error(
			w,
			"KasmVNC upstream unavailable",
			http.StatusBadGateway,
		)
	}

	return &vncProxy{
		target: targetURL,
		proxy:  proxy,
	}, nil
}

func (p *vncProxy) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	p.proxy.ServeHTTP(w, r)
}
