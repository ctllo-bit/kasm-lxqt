package main

import (
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration, mirroring the environment
// variables the Node.js backend consumed.
type Config struct {
	Subfolder      string
	Title          string
	FMHome         string
	VNCDir         string
	VNCProxyTarget string
	Port           int
	AudioDevice    string
	AudioServer    string
	MicSocket      string
	MaxUploadSize  int64
}

func loadConfig() Config {
	return Config{
		Subfolder:      envOr("SUBFOLDER", "/"),
		Title:          envOr("TITLE", "KasmVNC Client"),
		FMHome:         envOr("FM_HOME", "/home/abc"),
		VNCDir:         envOr("VNC_DIR", "/usr/share/kasmvnc/www"),
		VNCProxyTarget: envOr("VNC_PROXY_TARGET", "https://127.0.0.1:6901"),
		Port:           envInt("PORT", 6900),
		AudioDevice:    envOr("AUDIO_DEVICE", "kasm_sink.monitor"),
		AudioServer:    envOr("AUDIO_SERVER", "/run/user/1000/pulse/native"),
		MicSocket:      envOr("MIC_SOCK", "/defaults/mic.sock"),
		MaxUploadSize:  envInt64("MAX_UPLOAD_SIZE", 200000000),
	}
}

// VNCPath mirrors the Node.js PATH logic used to pass the subfolder
// through to the KasmVNC iframe so KasmVNC connects to /websockify.
func (c Config) VNCPath() string {
	if c.Subfolder == "/" {
		return ""
	}
	return "&path=" + strings.TrimPrefix(c.Subfolder, "/") + "/websockify"
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || os.Getenv(key) == "" {
		return fallback
	}
	return value
}

func envInt64(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil || os.Getenv(key) == "" {
		return fallback
	}
	return value
}
