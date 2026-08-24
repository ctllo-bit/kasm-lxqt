# Kclient 项目分析

## 1. 项目概览

Kclient 是 LinuxServer 维护的一个轻量级 Web 客户端，用 iframe 包装 [KasmVNC](https://github.com/kasmtech/KasmVNC)，
在此基础上为容器化的 webtop / VDI 环境补充两类能力：

- 音频：从容器内的 PulseAudio 采集桌面声音并推送到浏览器播放，同时支持浏览器麦克风输入。
- 文件管理：通过独立页面浏览、上传、下载、删除容器内的文件。

项目当前版本为 `0.4.1`，采用 GPL-3.0-or-later 许可证。README 已明确说明项目不再积极维护，欢迎 fork。

## 2. 技术栈

| 层次 | 技术 |
| --- | --- |
| 后端 | Node.js + Express 4 + EJS |
| 实时通信 | Socket.IO 4（文件管理、音频各一个独立 path） |
| 音频服务端 | pulseaudio2（采集 PulseAudio monitor 输出） |
| 前端 | 原生 JavaScript + jQuery，无构建工具 |
| 远程桌面 | KasmVNC iframe（`/vnc` 静态目录） |

依赖集中在 `package.json`：`express`、`ejs`、`pulseaudio2`、`socket.io`。
`body-parser` 在代码中虽然被 require，但实际没有被路由使用。

## 3. 目录结构

```text
kclient/
├── index.js                 # Express + Socket.IO 服务端
├── package.json
├── README.md
└── public/
    ├── index.html           # 主页面：KasmVNC iframe + 功能栏
    ├── manifest.json        # PWA manifest（EJS 模板渲染标题）
    ├── filebrowser.html     # 文件浏览器页面
    ├── js/
    │   ├── kclient.js       # 音频播放、麦克风、UI 控制
    │   ├── filebrowser.js   # 文件浏览器交互
    │   └── jquery.min.js
    ├── css/
    │   ├── kclient.css
    │   └── filebrowser.css
    └── icon.png / favicon.ico / 图标 SVG
```

## 4. 服务端架构（index.js）

服务端整体是一个 Express 应用，挂在 `SUBFOLDER` 前缀下，监听硬编码端口 `6900`：

1. `/public`：静态资源。
2. `/vnc`：静态代理到容器内的 `/usr/share/kasmvnc/www/`，承载 KasmVNC 页面。
3. `/`：EJS 渲染 `public/index.html`，注入标题和 KasmVNC path 参数。
4. `/files`：返回文件浏览器页面。
5. `/manifest.json`：渲染 PWA manifest。

同一个 HTTP server 上挂了两套 Socket.IO 服务：

- `files/socket.io`：文件管理协议，`maxHttpBufferSize` 为 200 MB。
- `audio/socket.io`：音频协议。

### 4.1 文件管理协议

客户端事件与服务端行为：

| 事件 | 行为 |
| --- | --- |
| `open` | 打开默认目录 `FM_HOME`（参数 password 被忽略，无实际鉴权） |
| `getfiles` | 列目录，区分文件夹与文件，返回 `renderfiles` |
| `downloadfile` | 整文件读入内存后通过 `sendfile` 回传 |
| `uploadfile` | 递归创建目录并写文件，成功后刷新列表 |
| `deletefiles` | 递归删除目录或删除文件，无任何警告 |
| `createfolder` | 创建目录 |

前端在 `filebrowser.js` 中提供：目录跳转、单文件/多文件上传、拖拽上传（递归解析 `webkitGetAsEntry`）、
下载（Blob + 临时 `<a>`）、删除、新建文件夹。上传侧有 200 MB 限制提示。

### 4.2 音频协议

音频输出方向：

1. 服务端用 `pulseaudio2` 从 `auto_null.monitor` 以 2 声道、44100 Hz、S16LE 采集。
2. 全零数据块被过滤，只发送非静音数据。
3. 浏览器端 `PCM` 播放器把 Int16 转为 Float32，按声道拆分后用 Web Audio 调度播放，
   并通过 `startTime` 排队消除卡顿；约 100 ms 无数据则重置缓冲区。

麦克风方向：

1. 浏览器通过 `getUserMedia` 获取麦克风。
2. 动态加载 AudioWorkletProcessor（字符串定义 + Blob URL），把 Float32 转成 Int16 后 `postMessage`。
3. 前端通过 `micdata` 事件发给服务端。
4. 服务端把数据写入 `/defaults/mic.sock`，交由容器侧的 PulseAudio 命名管道/FIFO 消费。

## 5. 配置项

| 环境变量 | 默认值 | 作用 |
| --- | --- | --- |
| `CUSTOM_USER` | `abc` | websockify 代理的 Basic Auth 用户名 |
| `PASSWORD` | `123456` | websockify 代理的 Basic Auth 密码 |
| `SUBFOLDER` | `/` | 应用挂载路径；非根路径时还会生成 KasmVNC iframe 的 path 参数 |
| `TITLE` | `KasmVNC Client` | 页面标题与 PWA manifest 标题 |
| `FM_HOME` | `/home/abc` | 文件浏览器打开和管理的根目录 |
| `VNC_DIR` | `/usr/share/kasmvnc/www` | KasmVNC 静态资源目录 |
| `VNC_PROXY_TARGET` | `https://127.0.0.1:8088` | websockify/VNC 上游目标 |
| `PORT` | `6900` | HTTP 监听端口 |
| `AUDIO_DEVICE` | `kasm_sink.monitor` | PulseAudio 采集设备 |
| `AUDIO_SERVER` | 空 | PulseAudio 服务端 socket，如 `/run/user/1000/pulse/native`；为空时自动探测 |
| `MIC_SOCK` | `/defaults/mic.sock` | 麦克风 PCM 写入目标 |
| `MAX_UPLOAD_SIZE` | `200000000` | 上传/下载大小上限 |

## 6. 前端交互

`index.html` 通过 iframe 加载：

```text
vnc/index.html?autoconnect=1&resize=remote&clipboard_up=true&clipboard_down=true
                &clipboard_seamless=true&show_control_bar=true[&path=...]
```

`kclient.js` 监听 KasmVNC 的 `postMessage`，响应 `control_open` / `control_close` /
`fullscreen`，控制顶部功能栏（文件管理、音频、麦克风）的显隐和全屏切换。

## 7. 主要风险与代码问题

### 7.1 安全：没有鉴权

- `CUSTOM_USER` / `PASSWORD` 定义了但没有被使用，`checkAuth` 实际直接放行。
- 任何能访问到该端口的客户端都可以浏览、上传、下载、删除文件。
- 文件管理没有把路径限制在 `FM_HOME` 内：`uploadfile`、`deletefiles`、`createfolder`、
  `downloadfile` 直接使用客户端传入的路径，可构造 `../` 或绝对路径越权访问宿主机文件。

### 7.2 健壮性

- 文件传输全部整文件读入内存，服务端没有字节数上限校验，大文件会占用大量内存，
  超过 Socket.IO `maxHttpBufferSize` 时传输失败。
- 删除操作没有任何确认步骤，且没有回收站机制。
- 文件名/路径中的单引号用 `|` 做临时替换，路径本身不做规范化，存在误替换和边界问题。
- 路径与设备大量硬编码：VNC 静态目录 `/usr/share/kasmvnc/www/`、音频设备
  `auto_null.monitor`、麦克风 socket `/defaults/mic.sock`、监听端口 `6900`。
  这意味着服务端基本只能在特定 LinuxServer 容器中直接运行。
- `getFiles` 出错时返回空列表而不是错误信息，用户难以判断是权限还是路径问题。
- 没有自动化测试，`npm test` 只是占位命令。

### 7.3 前端兼容性

- 音频处理对 Socket.IO 二进制数据做 TypedArray 转换时，隐含“字节长度等于采样数”
  的假设，与 Socket.IO 默认把 Buffer 解码为 ArrayBuffer 的行为不完全一致，属于脆弱点。
- 麦克风 `AudioContext` 未显式指定采样率，与后端 44100 Hz 的约定可能不一致。
- `message` 事件处理中直接引用全局 `event.data`，依赖浏览器全局事件对象，写法较旧。
- 拖拽上传依赖 `webkitGetAsEntry`，兼容范围有限。

## 8. 改进建议

1. 增加认证中间件，并让 `PASSWORD` / `CUSTOM_USER` 真正参与鉴权。
2. 文件操作前统一做路径规范化，确保所有路径都在 `FM_HOME` 之下。
3. 服务端限制文件大小、流式读写，避免整文件进内存。
4. 删除改为软删除或至少前端二次确认。
5. 把端口、VNC 目录、音频设备、麦克风 socket 路径全部提升为环境变量。
6. 补充单元测试和一次基础的 socket 集成测试。
7. 统一使用 `fs.promises` 的 `stat`，避免同步 `lstatSync` 阻塞事件循环。

## 9. Go 迁移记录

当前版本的后端已由 Node.js/Express/Socket.IO 重写为 Go，前端同步改为原生 WebSocket，
项目可以从 `go build -o kclient .` 构建。

### 9.1 新增与变更文件

| 文件 | 说明 |
| --- | --- |
| `main.go` | HTTP 路由、模板渲染、subfolder 挂载、服务启动 |
| `config.go` | 环境变量配置，新增 `PORT`、`VNC_DIR`、`AUDIO_DEVICE`、`MIC_SOCK`、`MAX_UPLOAD_SIZE` |
| `filebrowser.go` | 文件管理 WebSocket 服务与路径安全校验 |
| `audio.go` | 音频 WebSocket 服务，通过 `parec` 子进程采集 PulseAudio |
| `vncproxy.go` | KasmVNC `/websockify` WebSocket 反向代理，注入 Basic Auth |
| `main_test.go` | HTTP 路由与文件管理 WebSocket 集成测试 |
| `public/index.html`、`public/manifest.json` | EJS 语法改为 Go `html/template` |
| `public/js/kclient.js`、`public/js/filebrowser.js` | socket.io 客户端改为原生 WebSocket |

### 9.2 架构对应关系

| Node.js | Go |
| --- | --- |
| Express + EJS | `net/http` + `html/template` |
| `files/socket.io` | `/files/ws`（JSON 文本命令 + 二进制文件帧） |
| `audio/socket.io` | `/audio/ws`（JSON 文本命令 + 二进制 PCM 帧） |
| `pulseaudio2` | 调用 `parec` 子进程读取 S16LE PCM |
| 硬编码 VNC 目录 | `VNC_DIR` 环境变量 |
| 硬编码端口 6900 | `PORT` 环境变量 |
| Node `http.on('upgrade')` websockify 代理 | `vncproxy.go` + `VNC_PROXY_TARGET` |

### 9.3 行为差异

- 文件路径会解析符号链接并强制限制在 `FM_HOME` 内，防止目录穿越；原 Node 版本直接使用客户端路径。
- 上传和下载都有 `MAX_UPLOAD_SIZE` 服务端校验，不再只依赖前端 200 MB 限制。
- 空目录返回 `[]` 而不是 `null`，前端处理更直接。
- 前端移除了 socket.io 依赖，页面不再请求 `/socket.io/socket.io.js`。
- `/websockify` 连接会反向代理到 `VNC_PROXY_TARGET`，并带上 `CUSTOM_USER`/`PASSWORD` 的 Basic Auth。
- 音频采集依赖 `parec`，无 PulseAudio 环境（例如 Windows 本地）时会自动禁用音频功能。

## 10. 结论

Kclient 是一个目标明确的轻量 VNC 包装器：结构简单、入口集中，适合作为 LinuxServer
webtop 容器的配套组件使用。它的主要短板集中在安全边界（无鉴权、无路径约束）和运维
灵活性（大量硬编码）上。Go 迁移已经收敛了路径穿越和硬编码问题，并保留了文件管理与
音频的核心能力；如果继续维护，下一步应优先补上真实的鉴权流程。
