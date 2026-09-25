// ============================================================
// KasmVNC 集成：消息监听、全屏、Toggle（保留原逻辑）
// ============================================================

var eventMethod = window.addEventListener ? "addEventListener" : "attachEvent";
var eventer = window[eventMethod];
var messageEvent = eventMethod == "attachEvent" ? "onmessage" : "message";
eventer(messageEvent, function (e) {
  if (e.data && e.data.action) {
    switch (e.data.action) {
      case 'control_open':
        openToggle('#lsbar');
        break;
      case 'control_close':
        closeToggle('#lsbar');
        break;
      case 'fullscreen':
        fullscreen();
        break;
    }
  }
}, false);

function fullscreen() {
  if (document.fullscreenElement || document.mozFullScreenElement ||
      document.webkitFullscreenElement || document.msFullscreenElement) {
    if (document.exitFullscreen) {
      document.exitFullscreen();
    } else if (document.mozCancelFullScreen) {
      document.mozCancelFullScreen();
    } else if (document.webkitExitFullscreen) {
      document.webkitExitFullscreen();
    } else if (document.msExitFullscreen) {
      document.msExitFullscreen();
    }
  } else {
    if (document.documentElement.requestFullscreen) {
      document.documentElement.requestFullscreen();
    } else if (document.documentElement.mozRequestFullScreen) {
      document.documentElement.mozRequestFullScreen();
    } else if (document.documentElement.webkitRequestFullscreen) {
      document.documentElement.webkitRequestFullscreen(Element.ALLOW_KEYBOARD_INPUT);
    } else if (document.body.msRequestFullscreen) {
      document.body.msRequestFullscreen();
    }
  }
}

function openToggle(id) {
  if ($(id).is(":hidden")) {
    $(id).slideToggle(300);
  }
}
function closeToggle(id) {
  if ($(id).is(":visible")) {
    $(id).slideToggle(300);
  }
}
function toggle(id) {
  $(id).slideToggle(300);
}

// ============================================================
// 音频：WebSocket + Opus + WebCodecs + AudioWorklet
// ============================================================

var audioWS = null;
var audioCtx = null;
var audioDecoder = null;
var workletNode = null;

// ---------- 1) AudioWorklet 处理器（内联，避免额外静态文件路由） ----------
const PCM_PLAYER_WORKLET = `
class PCMPlayer extends AudioWorkletProcessor {
  constructor() {
    super();
    this.queue = [];
    this.current = null;
    this.offset = 0;
    this.minBufferSamples = 4800;    // ★ 100ms
    this.maxBufferSamples = 48000;   // ★ 1s，超过就丢旧帧
    this.port.onmessage = (ev) => { this.queue.push(ev.data.channels); };
  }
  process(inputs, outputs) {
    const out = outputs[0];
    const frames = out[0].length;

    let queued = 0;
    if (this.current) queued += this.current[0].length - this.offset;
    for (let i = 0; i < this.queue.length; i++) queued += this.queue[i][0].length;

    // ★ 水位过高：丢最旧帧，保证延迟不无限增长
    if (queued > this.maxBufferSamples) {
      while (this.queue.length > 0 && queued > this.minBufferSamples) {
        queued -= this.queue.shift()[0].length;
      }
    }

    if (queued < this.minBufferSamples) {
      for (let c = 0; c < out.length; c++) out[c].fill(0);
      return true;
    }

    let written = 0;
    while (written < frames) {
      if (!this.current) {
        if (this.queue.length === 0) break;
        this.current = this.queue.shift();
        this.offset = 0;
      }
      const remaining = this.current[0].length - this.offset;
      const n = Math.min(remaining, frames - written);
      for (let c = 0; c < out.length; c++) {
        const src = this.current[Math.min(c, this.current.length - 1)];
        out[c].set(src.subarray(this.offset, this.offset + n), written);
      }
      written += n;
      this.offset += n;
      if (this.offset >= this.current[0].length) this.current = null;
    }
    for (let c = 0; c < out.length; c++) {
      if (written < frames) out[c].fill(0, written);
    }
    return true;
  }
}
registerProcessor('pcm-player', PCMPlayer);
`;

// ---------- 2) URL 构造：兼容 / 与 /app/kasm-lxqt/ 两种页面路径 ----------
function buildAudioWSURL() {
  let base = window.location.pathname || '/';
  // 结尾不是 '/' 就去掉最后一段，补 '/'
  if (!base.endsWith('/')) {
    base = base.replace(/\/[^/]*$/, '/');
  }
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return proto + '//' + window.location.host + base + 'audio/ws';
}

// ---------- 3) 初始化 / 销毁 音频管线 ----------
async function ensureAudioPipeline() {
  if (audioDecoder) return;

  audioCtx = new (window.AudioContext || window.webkitAudioContext)({
    sampleRate: 48000,
  });
  if (audioCtx.state === 'suspended') {
    await audioCtx.resume();
  }

  const blob = new Blob([PCM_PLAYER_WORKLET], { type: 'application/javascript' });
  const url = URL.createObjectURL(blob);
  await audioCtx.audioWorklet.addModule(url);
  URL.revokeObjectURL(url);

  workletNode = new AudioWorkletNode(audioCtx, 'pcm-player', {
    numberOfInputs: 0,
    numberOfOutputs: 1,
    outputChannelCount: [2],
  });
  workletNode.connect(audioCtx.destination);

  audioDecoder = new AudioDecoder({
    output: (audioData) => {
      const ch = audioData.numberOfChannels;
      const frames = audioData.numberOfFrames;
      const planes = [];
      for (let c = 0; c < ch; c++) {
        const buf = new Float32Array(frames);
        audioData.copyTo(buf, { planeIndex: c, format: 'f32-planar' });
        planes.push(buf);
      }
      if (workletNode) {
        workletNode.port.postMessage({ channels: planes });
      }
      audioData.close();
    },
    error: (e) => console.error('AudioDecoder error:', e),
  });

  audioDecoder.configure({
    codec: 'opus',
    sampleRate: 48000,
    numberOfChannels: 2,
  });
}

function destroyAudioPipeline() {
  if (audioDecoder) { try { audioDecoder.close(); } catch (e) {} audioDecoder = null; }
  if (workletNode) { try { workletNode.disconnect(); } catch (e) {} workletNode = null; }
  if (audioCtx) { try { audioCtx.close(); } catch (e) {} audioCtx = null; }
}

// ---------- 4) 音频按钮 ----------
async function audio() {
  // 已开 → 关
  if (audioWS) {
    try { audioWS.send(JSON.stringify({ type: 'close' })); } catch (e) {}
    try { audioWS.close(); } catch (e) {}
    audioWS = null;
    destroyAudioPipeline();
    $('#audioButton').removeClass("icons-selected");
    return;
  }

  $('#audioButton').addClass("icons-selected");

  try {
    if (typeof AudioDecoder === 'undefined') {
      throw new Error('当前浏览器不支持 WebCodecs (AudioDecoder)，请使用 Chrome/Edge 94+ 或 Safari 16.4+');
    }

    await ensureAudioPipeline();

    const wsUrl = buildAudioWSURL();
    console.log('audio ws url =', wsUrl);

    const ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';
    audioWS = ws;

    let baseTs = null;
    let recvSeq = 0;

    ws.onopen = () => {
      // 兼容旧协议：主动通知后端开始推流（后端如果是一连接就推，这条消息会被忽略）
      try { ws.send(JSON.stringify({ type: 'open' })); } catch (e) {}
    };

    ws.onmessage = (ev) => {
      const buf = ev.data;
      if (!(buf instanceof ArrayBuffer) || buf.byteLength <= 12) return;

      const dv = new DataView(buf);
      const seq = dv.getUint32(0, false);              // big-endian
      const tsMs = Number(dv.getBigUint64(4, false));  // big-endian
      if (baseTs === null) baseTs = tsMs;

      if (recvSeq !== 0 && seq !== recvSeq) {
        console.warn('audio gap: expect', recvSeq, 'got', seq);
      }
      recvSeq = seq + 1;

      const opusData = new Uint8Array(buf, 12);
      try {
        audioDecoder.decode(new EncodedAudioChunk({
          type: 'key',
          timestamp: (tsMs - baseTs) * 1000, // 微秒
          duration: 20000,                    // 20ms
          data: opusData,
        }));
      } catch (e) {
        console.error('decode enqueue:', e);
      }
    };

    ws.onerror = (e) => console.error('audio ws error:', e);

    ws.onclose = () => {
      console.log('audio ws closed');
      if (audioWS === ws) {
        audioWS = null;
        destroyAudioPipeline();
        $('#audioButton').removeClass("icons-selected");
      }
    };
  } catch (err) {
    console.error('audio error:', err);
    if (audioWS) { try { audioWS.close(); } catch (e) {} audioWS = null; }
    destroyAudioPipeline();
    $('#audioButton').removeClass("icons-selected");
  }
}

// ============================================================
// 麦克风：暂不启用（后端目前只做下行）
// ============================================================
function mic() {
  console.warn('mic not implemented yet');
}