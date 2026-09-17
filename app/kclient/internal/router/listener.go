package router

import (
	"errors"
	"fmt"
	"kclient/config"
	"net"
	"os"
	"strconv"
)

// 初始化创建 Listener
func CreateListener(cfg config.Config) (net.Listener, error) {
	// ------------------------------------------------------------
	// Unix Socket
	// ------------------------------------------------------------
	if cfg.Socket != "" {
		socketPath := cfg.Socket

		// 删除启动前残留的旧 Socket
		if err := os.Remove(socketPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove old unix socket %s: %w", socketPath, err)
		}

		// 创建 Unix Socket
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("listen unix socket %s: %w", socketPath, err)
		}

		// 设置 Socket 权限
		if err := os.Chmod(socketPath, 0660); err != nil {
			_ = listener.Close()
			_ = os.Remove(socketPath)

			return nil, fmt.Errorf("chmod unix socket %s: %w", socketPath, err)
		}

		return listener, nil
	}

	// ------------------------------------------------------------
	// TCP + HTTPS
	// ------------------------------------------------------------

	// 先检查 TLS 配置，再打开 TCP 端口
	if cfg.SSL.CertFile == "" {
		return nil, errors.New("HTTPS certificate file is empty")
	}

	if cfg.SSL.KeyFile == "" {
		return nil, errors.New("HTTPS private key file is empty")
	}

	addr := ":" + strconv.Itoa(cfg.VNC.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen tcp %s: %w", addr, err)
	}

	return listener, nil
}
