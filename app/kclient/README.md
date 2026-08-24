# Kclient

Simple iframe wrapper for the [KasmVNC](https://github.com/kasmtech/KasmVNC) protocol to add audio and file
management. The backend has been rewritten from Node.js/Express/Socket.IO to Go; the frontend now talks to the
backend over native WebSocket.

## Features

- KasmVNC iframe wrapper with file manager, audio and microphone controls
- Web file browser: browse, upload, download, delete and create folders
- Desktop audio captured from PulseAudio (`parec`) and streamed to the browser
- Browser microphone captured with an AudioWorklet and written to a PulseAudio socket
- KasmVNC `/websockify` WebSocket reverse proxy with Basic auth injection

## Build

```sh
go build -o kclient .
```

## Run

```sh
FM_HOME=/config ./kclient
```

The KasmVNC web assets are expected at `/usr/share/kasmvnc/www` by default, matching the LinuxServer container
layout. Set `VNC_DIR` if they live elsewhere.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `CUSTOM_USER` | `abc` | Reserved for authentication |
| `PASSWORD` | `123456` | Basic auth password for the KasmVNC proxy |
| `SUBFOLDER` | `/` | URL prefix for the whole app |
| `TITLE` | `KasmVNC Client` | Page and manifest title |
| `FM_HOME` | `/home/abc` | File browser root |
| `VNC_DIR` | `/usr/share/kasmvnc/www` | KasmVNC static assets |
| `VNC_PROXY_TARGET` | `https://127.0.0.1:8088` | Upstream websockify/VNC target |
| `PORT` | `6900` | HTTP listen port |
| `AUDIO_DEVICE` | `kasm_sink.monitor` | PulseAudio monitor source |
| `AUDIO_SERVER` | empty | PulseAudio server socket, e.g. `/run/user/1000/pulse/native`; auto-detected when empty |
| `MIC_SOCK` | `/defaults/mic.sock` | Destination for browser microphone PCM |
| `MAX_UPLOAD_SIZE` | `200000000` | Upload/download size limit in bytes |

## WebSocket protocol

Native WebSocket replaces the old Socket.IO transport:

- `/files/ws` uses JSON text commands plus binary frames for file payloads
- `/audio/ws` uses JSON text commands plus binary PCM frames

File commands: `open`, `getfiles`, `upload`, `download`, `delete`, `createfolder`.
Audio commands: `open`, `close`; binary frames from the server carry desktop audio, binary frames to the server
carry microphone data.

## Tests

```sh
go test ./...
```
