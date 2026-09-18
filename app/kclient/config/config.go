package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Subfolder string `yaml:"subfolder"`
	Title     string `yaml:"title"`

	AuthEnabled bool `yaml:"auth"`

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

func Load(home string) Config {
	// 设置硬编码的默认值
	cfg := Config{
		Subfolder:     "/",
		Title:         "KasmVNC Client",
		MaxUploadSize: 200000000,
	}

	// 读取 YAML 文件
	data, err := os.ReadFile(home + "/.vnc/kclient.yaml")
	if err != nil {
		return cfg // 读不到返回默认值
	}

	// YAML 覆盖默认值
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg
	}

	return cfg
}

// 根据 Subfolder 计算出 KasmVNC iframe 需要的路径参数
func (c Config) VNCPath() string {
	prefix := strings.Trim(c.Subfolder, "/")
	if prefix == "" {
		return "websockify"
	}

	return prefix + "/websockify"
}
