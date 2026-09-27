'use strict';

// ============================================================
// 工具函数
// ============================================================
function q(sel) { return document.querySelector(sel); }

function showEl(sel) { const el = q(sel); if (el) el.style.display = 'block'; }
function hideEl(sel) { const el = q(sel); if (el) el.style.display = 'none'; }
function toggleEl(sel) {
  const el = q(sel);
  if (!el) return;
  el.style.display = (el.style.display === 'none' || el.style.display === '') ? 'block' : 'none';
}

// ============================================================
// KasmVNC iframe 通信
// ============================================================
// 给 KasmVNC iframe 发消息（同源，用 location.origin 作为 targetOrigin）
function sendVncMessage(message) {
  const frame = q('iframe.vnc');
  if (!frame || !frame.contentWindow) {
    console.warn('KasmVNC iframe not found');
    return false;
  }
  frame.contentWindow.postMessage(message, window.location.origin);
  return true;
}

// 监听 KasmVNC iframe 发来的控制指令
window.addEventListener('message', (e) => {
  const data = e.data;
  if (!data || !data.action) return;

  switch (data.action) {
    case 'control_open':  showEl('#lsbar'); break;
    case 'control_close': hideEl('#lsbar'); break;
    case 'fullscreen':    toggleFullscreen(); break;
  }
}, false);

// ============================================================
// 全屏切换
// ============================================================
function isFullscreen() {
  return !!(document.fullscreenElement
    || document.webkitFullscreenElement
    || document.mozFullScreenElement
    || document.msFullscreenElement);
}

function enterFullscreen() {
  const el = document.documentElement;
  const req = el.requestFullscreen
    || el.webkitRequestFullscreen
    || el.mozRequestFullScreen
    || el.msRequestFullscreen;
  if (!req) return Promise.reject(new Error('Fullscreen API unavailable'));
  return Promise.resolve(req.call(el));
}

function exitFullscreen() {
  const ex = document.exitFullscreen
    || document.webkitExitFullscreen
    || document.mozCancelFullScreen
    || document.msExitFullscreen;
  if (!ex) return Promise.resolve();
  return Promise.resolve(ex.call(document));
}

function toggleFullscreen() {
  if (isFullscreen()) { 
    exitFullscreen(); 
    return; 
  }

  // 原始模式是 remote，进入全屏后必须先切到 scale，否则 KasmVNC 会清除 forcedResolutionX/Y
  sendVncMessage({ action: 'resize', value: 'scale' });

  let realWidth  = Math.round(screen.width  * window.devicePixelRatio);
  let realHeight = Math.round(screen.height * window.devicePixelRatio);
  sendVncMessage({action: 'set_resolution',value_x: realWidth,value_y: realHeight});

  enterFullscreen().catch((err) => {
    console.warn('[kasm] fullscreen failed:', err);
    // 失败回滚，避免 VNC 卡在 scale
    sendVncMessage({ action: 'resize', value: 'remote' });
  });
}

// 退出全屏（ESC / 浏览器行为）后恢复 remote 模式
['fullscreenchange', 'webkitfullscreenchange', 'mozfullscreenchange', 'MSFullscreenChange']
  .forEach((evt) => document.addEventListener(evt, () => {
    if (!isFullscreen()) sendVncMessage({ action: 'resize', value: 'remote' });
  }));

// ============================================================
// PCM 播放器
// 每个实例自己持有状态，不再用全局 buffer/playing/lock
// ============================================================
function PCM() {
  this.buffer = [];        // 交错立体声 float 样本（L,R,L,R,...）
  this.playing = false;
  this.lock = false;       // 本轮 100ms 内是否收到过数据
  this._timer = null;
  this.init();
}

PCM.prototype.init = function () {
  const Ctx = window.AudioContext || window.webkitAudioContext;
  this.audioCtx = new Ctx({ sampleRate: 44100 });
  this.audioCtx.resume();

  this.gainNode = this.audioCtx.createGain();
  this.gainNode.gain.value = 1;
  this.gainNode.connect(this.audioCtx.destination);

  this.startTime = this.audioCtx.currentTime;

  // 检测音频流停止：连续 100ms 没有新数据就清空
  const self = this;
  this._timer = setInterval(function () {
    if (self.playing) {
      if (!self.lock) {
        self.buffer = [];
        self.playing = false;
      }
      self.lock = false;
    }
  }, 100);
};

PCM.prototype.feed = function (data) {
  // data: ArrayBuffer，16-bit LE 交错立体声
  const i16 = new Int16Array(data);
  for (let i = 0; i < i16.length; i++) {
    this.buffer.push(i16[i] / 32767);
  }
  this.lock = true;

  const total = this.buffer.length;
  const frames = total >> 1;                // 每帧 2 个样本（L+R）
  if (frames === 0) return;

  const duration = frames / 44100;
  if (duration < 0.05 && !this.playing) return;
  this.playing = true;

  const buf = this.audioCtx.createBuffer(2, frames, 44100);
  const left = buf.getChannelData(0);
  const right = buf.getChannelData(1);
  for (let i = 0, j = 0; i < frames; i++, j += 2) {
    left[i] = this.buffer[j];
    right[i] = this.buffer[j + 1];
  }
  this.buffer = [];

  if (this.startTime < this.audioCtx.currentTime) {
    this.startTime = this.audioCtx.currentTime;
  }

  const src = this.audioCtx.createBufferSource();
  src.buffer = buf;
  src.connect(this.gainNode);
  src.start(this.startTime);
  this.startTime += duration;
};

PCM.prototype.destroy = function () {
  if (this._timer) { clearInterval(this._timer); this._timer = null; }
  this.buffer = [];
  this.playing = false;
  if (this.audioCtx) {
    this.audioCtx.close();
    this.audioCtx = null;
  }
};

// ============================================================
// 音频 Socket.IO 连接
// ============================================================
const audioSocket = io(
  `${window.location.protocol}//${window.location.hostname}:${window.location.port}`,
  { path: `${window.location.pathname}audio/socket.io` }
);

let player = null;
let micEnabled = false;
let micWorkletNode = null;   // AudioWorkletNode
let micSource = null;        // MediaStreamAudioSourceNode
let micStream = null;        // getUserMedia 返回的 MediaStream
let micCtx = null;           // 麦克风专用 AudioContext
let micWorkletURL = null;    // Blob URL，用于 revoke

// 播放开关：正在播 → 停；未播 → 开
function audio() {
  const btn = q('#audioButton');

  if (player && player.audioCtx) {
    player.destroy();
    player = null;
    audioSocket.emit('close', '');
    if (btn) btn.classList.remove('icons-selected');
    return;
  }

  audioSocket.emit('open', '');
  player = new PCM();
  if (btn) btn.classList.add('icons-selected');
}

// 服务端推来的音频帧
function processAudio(data) {
  if (player && player.audioCtx) player.feed(data);
}

audioSocket.on('audio', processAudio);

// ============================================================
// AudioWorklet：把麦克风 float 采样转成 int16，回传主线程
// （跑在音频线程，不是主线程）
// ============================================================
const micWorkletProcessorCode = `
class MicWorkletProcessor extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs[0];
    if (!input || !input[0]) return true;

    const ch = input[0];
    const out = new Int16Array(ch.length);
    let allZero = true;

    for (let i = 0; i < ch.length; i++) {
      let v = ch[i];
      if (v > 1) v = 1;
      else if (v < -1) v = -1;                    // clamp，防溢出回绕
      const s = v < 0 ? v * 0x8000 : v * 0x7fff;  // 负数走 -32768，正数走 32767
      out[i] = s;
      if (s !== 0) allZero = false;
    }

    if (!allZero) {
      // transfer buffer，零拷贝
      this.port.postMessage({ buffer: out.buffer }, [out.buffer]);
    }
    return true;
  }
}
registerProcessor('mic-worklet-processor', MicWorkletProcessor);
`;

// ============================================================
// 释放麦克风相关资源（关闭时 / 出错回滚时共用）
// ============================================================
async function cleanupMic() {
  if (micSource) {
    try { micSource.disconnect(); } catch (_) {}
    micSource = null;
  }
  if (micWorkletNode) {
    try { micWorkletNode.disconnect(); } catch (_) {}
    micWorkletNode.port.onmessage = null;
    micWorkletNode = null;
  }
  if (micStream) {
    micStream.getTracks().forEach((t) => t.stop());  // 关掉麦克风指示灯
    micStream = null;
  }
  if (micCtx) {
    try { await micCtx.close(); } catch (_) {}
    micCtx = null;
  }
  if (micWorkletURL) {
    URL.revokeObjectURL(micWorkletURL);
    micWorkletURL = null;
  }
}

// ============================================================
// 麦克风开关
// ============================================================
async function mic() {
  const btn = q('#micButton');

  // —— 关闭 ——
  if (micEnabled) {
    micEnabled = false;
    if (btn) btn.classList.remove('icons-selected');
    await cleanupMic();
    return;
  }

  // —— 打开前先检查安全上下文 ——
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    alert(
      '麦克风不可用：当前页面不是安全上下文。\n' +
      '请改用 https:// 或 http://localhost 访问。'
    );
    return;
  }

  // —— 打开 ——
  micEnabled = true;
  if (btn) btn.classList.add('icons-selected');

  try {
    micStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    micCtx = new (window.AudioContext || window.webkitAudioContext)();

    const blob = new Blob([micWorkletProcessorCode], { type: 'text/javascript' });
    micWorkletURL = URL.createObjectURL(blob);
    await micCtx.audioWorklet.addModule(micWorkletURL);

    micWorkletNode = new AudioWorkletNode(micCtx, 'mic-worklet-processor');
    micWorkletNode.port.onmessage = (e) => {
      audioSocket.emit('micdata', e.data.buffer);
    };

    micSource = micCtx.createMediaStreamSource(micStream);
    micSource.connect(micWorkletNode);
  } catch (err) {
    console.error('[mic] error', err);
    micEnabled = false;
    if (btn) btn.classList.remove('icons-selected');
    await cleanupMic();          // 回滚已创建的部分资源
  }
}

// ============================================================
// DOM 就绪后绑定按钮
// ============================================================
document.addEventListener('DOMContentLoaded', function () {
  q('#fileButton').addEventListener('click', function () { toggleEl('#files'); });
  q('#files .close').addEventListener('click', function () { hideEl('#files'); });
  q('#audioButton').addEventListener('click', function () { audio(); });
  q('#micButton').addEventListener('click', function () { mic(); });
});