// 监听 KasmVNC iframe 发来的控制指令
window.addEventListener('message', function (e) {
  var data = e.data;
  if (!data || !data.action) return;

  switch (data.action) {
    case 'control_open':
      showEl('#lsbar');
      break;
    case 'control_close':
      hideEl('#lsbar');
      break;
    case 'fullscreen':
      toggleFullscreen();
      break;
  }
});

// 工具函数
function q(sel) { return document.querySelector(sel); }
function showEl(sel) { var el = q(sel); if (el) el.style.display = 'block'; }
function hideEl(sel) { var el = q(sel); if (el) el.style.display = 'none'; }
function toggleEl(sel) {
  var el = q(sel);
  if (!el) return;
  el.style.display = (el.style.display === 'none' || el.style.display === '') ? 'block' : 'none';
}

// 给 KasmVNC iframe 发消息
function sendVncMessage(message) {
  const frame = q('iframe.vnc');
  if (!frame || !frame.contentWindow) {
    console.warn('KasmVNC iframe not found');
    return false;
  }
  frame.contentWindow.postMessage(message, '*');
  return true;
}

// 全屏切换
function toggleFullscreen() {
  if (document.fullscreenElement) {
    document.exitFullscreen();
    return;
  }
  // 原始模式是 remote，进入全屏后必须先切到 scale，否则 KasmVNC 会清除 forcedResolutionX/Y。
  sendVncMessage({action: 'resize',value: 'scale'});

  // 使用 KasmVNC 自己的 set_resolution
  let realWidth  = Math.round(screen.width  * window.devicePixelRatio);
  let realHeight = Math.round(screen.height * window.devicePixelRatio);
  sendVncMessage({action: 'set_resolution',value_x: realWidth,value_y: realHeight});

  // 进入全屏
  document.documentElement.requestFullscreen();
}

// 退出全屏（点击 / ESC / 浏览器行为）后恢复 remote 模式
document.addEventListener('fullscreenchange', () => {
  if (!document.fullscreenElement) {
    sendVncMessage({action: 'resize',value: 'remote'});
  }
});

// DOM 就绪后一次性绑定按钮
document.addEventListener('DOMContentLoaded', function () {
  q('#fileButton').addEventListener('click', function () { toggleEl('#files'); });
  q('#files .close').addEventListener('click', function () { hideEl('#files'); });
  q('#audioButton').addEventListener('click', function () { audio(); });
  q('#micButton').addEventListener('click', function () { mic(); });
});







//// PCM player ////
var buffer = [];
var playing = false;
var lock = false;
// Check for audio stop to reset buffer
setInterval(function() {
  if (playing) {
    if (!lock) {
      buffer = [];
      playing = false;
    }
    lock = false;
  }
}, 100);
function PCM() {
  this.init()
}
// Player Init
PCM.prototype.init = function() {
  // Establish audio context
  this.audioCtx = new(window.AudioContext || window.webkitAudioContext)({
    sampleRate: 44100
  })
  this.audioCtx.resume()
  this.gainNode = this.audioCtx.createGain()
  this.gainNode.gain.value = 1
  this.gainNode.connect(this.audioCtx.destination)
  this.startTime = this.audioCtx.currentTime
}
// Stereo player
PCM.prototype.feed = function(data) {
  lock = true;
  // Convert bytes to typed array then float32 array
  let i16Array = new Int16Array(data, 0, data.length);
  let f32Array = Float32Array.from(i16Array, x => x / 32767);
  buffer = new Float32Array([...buffer, ...f32Array]);
  let buffAudio = this.audioCtx.createBuffer(2, buffer.length, 44100);
  let duration = buffAudio.duration / 2;
  if ((duration > .05) || (playing)) {
    playing = true;
    let buffSource = this.audioCtx.createBufferSource();
    let arrLength = buffer.length / 2;
    let left = buffAudio.getChannelData(0);
    let right = buffAudio.getChannelData(1);
    let byteCount = 0;
    let offset = 1;
    for (let count = 0; count < arrLength; count++) {
      left[count] = buffer[byteCount];
      byteCount += 2;
      right[count] = buffer[offset];
      offset += 2;
    }
    buffer = [];
    if (this.startTime < this.audioCtx.currentTime) {
      this.startTime = this.audioCtx.currentTime;
    }
    buffSource.buffer = buffAudio;
    buffSource.connect(this.gainNode);
    buffSource.start(this.startTime);
    this.startTime += duration;
  }
}
// Destroy player
PCM.prototype.destroy = function() {
  buffer = [];
  playing = false;
  this.audioCtx.close();
  this.audioCtx = null;
};


// Websocket comms for audio
var host = window.location.hostname;
var port = window.location.port;
var protocol = window.location.protocol;
var path = window.location.pathname;
var socket = io(protocol + '//' + host + ':' + port, { path: path + 'audio/socket.io'});
var player = {};
var micEnabled = false;
var micWorkletNode; // To store the AudioWorkletNode
var audio_context;

function audio() {
  if (('audioCtx' in player) && (player.audioCtx)) {
    player.destroy();
    socket.emit('close', '');
    $('#audioButton').removeClass("icons-selected");
    return;
  }
  socket.emit('open', '');
  player = new PCM();
  $('#audioButton').addClass("icons-selected");
}

function processAudio(data) {
  player.feed(data);
}

socket.on('audio', processAudio);

// Define the AudioWorkletProcessor as a string.
const micWorkletProcessorCode = `
class MicWorkletProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
  }

  process(inputs, outputs, parameters) {
    const input = inputs[0];

    if (input && input[0]) { // Check if input and channel data are available
      const inputChannelData = input[0];
      const int16Array = Int16Array.from(inputChannelData, x => x * 32767);
      if (! int16Array.every(item => item === 0)) {
        this.port.postMessage({ buffer: int16Array.buffer });
      }
    }
    return true; // Keep the processor alive
  }
}

registerProcessor('mic-worklet-processor', MicWorkletProcessor);
`;

async function mic() {
  if (micEnabled) {
    $('#micButton').removeClass("icons-selected");
    if (micWorkletNode) {
      micWorkletNode.disconnect();
      micWorkletNode = null; // Release the node
    }
    if (audio_context) {
      audio_context.close();
      audio_context = null;
    }
    micEnabled = false;
    return;
  }
  $('#micButton').addClass("icons-selected");
  micEnabled = true;
  var mediaConstraints = {
    audio: true
  };

  try {
    const stream = await navigator.mediaDevices.getUserMedia(mediaConstraints);
    audio_context = new window.AudioContext();

    // Create a URL for the AudioWorkletProcessor code
    const micWorkletProcessorBlob = new Blob([micWorkletProcessorCode], { type: 'text/javascript' });
    const micWorkletProcessorURL = URL.createObjectURL(micWorkletProcessorBlob);

    await audio_context.audioWorklet.addModule(micWorkletProcessorURL);

    micWorkletNode = new AudioWorkletNode(audio_context, 'mic-worklet-processor');

    micWorkletNode.port.onmessage = (event) => {
      socket.emit('micdata', event.data.buffer);
    };

    let source = audio_context.createMediaStreamSource(stream);
    source.connect(micWorkletNode);

  } catch (e) {
    console.error('media error', e);
    $('#micButton').removeClass("icons-selected");
    micEnabled = false;
  }
}