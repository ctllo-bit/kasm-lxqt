package main

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Subfolder string `yaml:"subfolder"`
	Title     string `yaml:"title"`

	FMHome string `yaml:"fm_home"`

	VNC struct {
		Port        int    `yaml:"port"`
		ProxyTarget string `yaml:"proxy_target"`
	} `yaml:"vnc"`

	Socket string `yaml:"socket"`

	SSL struct {
		CertFile string `yaml:"pem_certificate"`
		KeyFile  string `yaml:"pem_key"`
	} `yaml:"ssl"`

	Audio struct {
		Device string `yaml:"device"`
		Server string `yaml:"server"`
	} `yaml:"audio"`

	MicSocket string `yaml:"mic_socket"`

	MaxUploadSize int64 `yaml:"max_upload_size"`
}

func loadConfig() Config {

	cfg := Config{
		Subfolder:     "/",
		Title:         "KasmVNC Client",
		FMHome:        "/home/remote-desktop",
		Socket:        "",
		MaxUploadSize: 200000000,
	}

	data, err := os.ReadFile(cfg.FMHome + "/.vnc/kclient.yaml")

	if err != nil {
		return cfg
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg
	}

	return cfg
}

// VNCPath mirrors the Node.js PATH logic used to pass the subfolder
// through to the KasmVNC iframe so KasmVNC connects to /websockify.
func (c Config) VNCPath() string {
	prefix := strings.Trim(c.Subfolder, "/")
	if prefix == "" {
		return "websockify"
	}

	return prefix + "/websockify"
}
