package listener

import (
	"errors"
	"net"
	"os"
	"strconv"
)

// Options 描述监听参数
type Options struct {
	Socket string // 非空 → Unix Socket
	Port   int    // 空 Socket 时用于 TCP
}

func New(opt Options) (net.Listener, error) {
	if opt.Socket != "" {
		return createSocket(opt.Socket)
	}
	return createTCP(opt.Port)
}

func createTCP(vncPort int) (net.Listener, error) {
	addr := ":" + strconv.Itoa(vncPort)
	return net.Listen("tcp", addr)
}

func createSocket(socketPath string) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0660); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}
