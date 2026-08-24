// Parse messages from KasmVNC
var eventMethod = window.addEventListener ? "addEventListener" : "attachEvent";
var eventer = window[eventMethod];
var messageEvent = eventMethod == "attachEvent" ? "onmessage" : "message";
eventer(messageEvent,function(e) {
  var data = e.data || event.data;
  if (data && data.action) {
    switch (data.action) {
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
},false);

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
  let sampleCount = Math.floor(data.byteLength / 2);
  if (sampleCount === 0) return;
  let i16Array = new Int16Array(data, 0, sampleCount);
  let f32Array = Float32Array.from(i16Array, x => x / 32767);
  let combined = new Float32Array(buffer.length + f32Array.length);
  combined.set(buffer, 0);
  combined.set(f32Array, buffer.length);
  buffer = combined;

  // Only schedule complete stereo frames and keep the remainder buffered
  let usable = buffer.length - (buffer.length % 2);
  if (usable === 0) return;
  let buffAudio = this.audioCtx.createBuffer(2, usable / 2, 44100);
  let duration = buffAudio.duration;
  if ((duration > .05) || (playing)) {
    playing = true;
    let buffSource = this.audioCtx.createBufferSource();
    let left = buffAudio.getChannelData(0);
    let right = buffAudio.getChannelData(1);
    let count = 0;
    for (let offset = 0; offset < usable; offset += 2) {
      left[count] = buffer[offset];
      right[count] = buffer[offset + 1];
      count++;
    }
    buffer = buffer.slice(usable);
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

// Handle Toggle divs
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

// Fullscreen handler
function fullscreen() {
  if (document.fullscreenElement || document.mozFullScreenElement || document.webkitFullscreenElement || document.msFullscreenElement) {
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

//// WebSocket comms for audio ////
var host = window.location.hostname;
var port = window.location.port;
var protocol = window.location.protocol;
var path = window.location.pathname.replace(/\/+$/, '');
var wsProtocol = protocol === 'https:' ? 'wss://' : 'ws://';
var socket = null;
var socketConnected = false;
var audioOpen = false;
var player = {};
var micEnabled = false;
var micWorkletNode; // To store the AudioWorkletNode
var audio_context;

function connectAudioSocket() {
  socket = new WebSocket(wsProtocol + host + ':' + port + (path ? path + '/' : '/') + 'audio/ws');
  socket.binaryType = 'arraybuffer';
  socket.onopen = function() {
    socketConnected = true;
    if (audioOpen) {
      socket.send(JSON.stringify({type: 'open'}));
    }
  };
  socket.onmessage = function(event) {
    if (typeof event.data === 'string') return;
    if (('audioCtx' in player) && (player.audioCtx)) {
      processAudio(event.data);
    }
  };
  socket.onclose = function() {
    socketConnected = false;
    setTimeout(connectAudioSocket, 3000);
  };
}
connectAudioSocket();

function audio() {
  if (('audioCtx' in player) && (player.audioCtx)) {
    player.destroy();
    audioOpen = false;
    if (socketConnected) {
      socket.send(JSON.stringify({type: 'close'}));
    }
    $('#audioButton').removeClass("icons-selected");
    return;
  }
  audioOpen = true;
  if (socketConnected) {
    socket.send(JSON.stringify({type: 'open'}));
  }
  player = new PCM();
  $('#audioButton').addClass("icons-selected");
}

function processAudio(data) {
  player.feed(data);
}

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
    audio_context = new window.AudioContext({sampleRate: 44100});

    // Create a URL for the AudioWorkletProcessor code
    const micWorkletProcessorBlob = new Blob([micWorkletProcessorCode], { type: 'text/javascript' });
    const micWorkletProcessorURL = URL.createObjectURL(micWorkletProcessorBlob);

    await audio_context.audioWorklet.addModule(micWorkletProcessorURL);

    micWorkletNode = new AudioWorkletNode(audio_context, 'mic-worklet-processor');

    micWorkletNode.port.onmessage = (event) => {
      if (socketConnected && socket.readyState === WebSocket.OPEN) {
        socket.send(event.data.buffer);
      }
    };

    let source = audio_context.createMediaStreamSource(stream);
    source.connect(micWorkletNode);

  } catch (e) {
    console.error('media error', e);
    $('#micButton').removeClass("icons-selected");
    micEnabled = false;
  }
}
