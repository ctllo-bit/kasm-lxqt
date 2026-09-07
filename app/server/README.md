# Kclient

Kclient 是 [KasmVNC](https://github.com/kasmtech/KasmVNC) 的 Go Web 包装层，为容器化远程桌面补充文件管理、扬声器音频和麦克风输入。

## 运行

要求 Go 1.26+、可访问的 KasmVNC Web 资源，以及（可选）提供 `parec` 的 PulseAudio 环境：

```sh
go run .
```

默认监听 `:6900`。可通过环境变量配置：

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:6900` | HTTP 监听地址。 |
| `SUBFOLDER` | `/` | 反向代理使用的应用 URL 前缀。 |
| `TITLE` | `KasmVNC Client` | 页面与 PWA 标题。 |
| `FM_HOME` | `/config` | 文件管理的受限根目录。 |
| `KASMVNC_WEB_ROOT` | `/usr/share/kasmvnc/www` | KasmVNC Web 静态资源目录。 |
| `MIC_PATH` | `/defaults/mic.sock` | 麦克风 PCM 数据的输出文件。 |
| `PULSE_RECORD_COMMAND` | `parec --device=auto_null.monitor --format=s16le --rate=44100 --channels=2` | 获取桌面 PCM 音频的命令。 |

服务端和前端通过标准 WebSocket 通信；不再依赖 Node.js 或 Socket.IO。详见 [架构说明](ARCHITECTURE.md)。
