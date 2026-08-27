package main

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Subfolder string `yaml:"subfolder"`
	Title     string `yaml:"title"`

	FMHome string `yaml:"fm_home"`

	VNC struct {
		Dir         string `yaml:"dir"`
		ProxyTarget string `yaml:"proxy_target"`
	} `yaml:"vnc"`

	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`

	Audio struct {
		Device string `yaml:"device"`
		Server string `yaml:"server"`
	} `yaml:"audio"`

	MicSocket string `yaml:"mic_socket"`

	MaxUploadSize int64 `yaml:"max_upload_size"`
}

func loadConfig() Config {

	cfg := Config{
		Subfolder: "/",
		Title:     "KasmVNC Client",

		FMHome: "/home/kasm",

		Server: struct {
			Port int `yaml:"port"`
		}{
			Port: 6900,
		},

		MaxUploadSize: 200000000,
	}

	data, err := os.ReadFile(
		"/home/kasm/.vnc/kclient.yaml",
	)

	if err != nil {
		return cfg
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg
	}

	return cfg
}

// func loadConfig() Config {
// 	return Config{
// 		Subfolder:      envOr("SUBFOLDER", "/"),
// 		Title:          envOr("TITLE", "KasmVNC Client"),
// 		FMHome:         envOr("FM_HOME", "/home/kasm"),
// 		VNCDir:         envOr("VNC_DIR", "/usr/share/kasmvnc/www"),
// 		VNCProxyTarget: envOr("VNC_PROXY_TARGET", "https://127.0.0.1:6901"),
// 		Port:           envInt("PORT", 6900),
// 		AudioDevice:    envOr("AUDIO_DEVICE", "kasm_sink.monitor"),
// 		AudioServer:    envOr("AUDIO_SERVER", "/run/user/1001/pulse/native"),
// 		MicSocket:      envOr("MIC_SOCK", "/defaults/mic.sock"),
// 		MaxUploadSize:  envInt64("MAX_UPLOAD_SIZE", 200000000),
// 	}
// }

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
