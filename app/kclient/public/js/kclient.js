// Parse messages from KasmVNC
var eventMethod = window.addEventListener ? "addEventListener" : "attachEvent";
var eventer = window[eventMethod];
var messageEvent = eventMethod == "attachEvent" ? "onmessage" : "message";
eventer(messageEvent,function(e) {
  if (event.data && event.data.action) {
    switch (event.data.action) {
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

//// WebRTC audio (recvonly) ////
var audioPc = null;
var audioEl = null;

async function audio() {
  // 已开 -> 关
  if (audioPc) {
    audioPc.close();
    audioPc = null;
    if (audioEl) { audioEl.srcObject = null; audioEl = null; }
    $('#audioButton').removeClass("icons-selected");
    return;
  }

  $('#audioButton').addClass("icons-selected");

  try {
    const pc = new RTCPeerConnection({
      // 跨机器时建议加 STUN；同机/同网段可留空
      // iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
    });
    audioPc = pc;

    pc.addTransceiver('audio', { direction: 'recvonly' });

    pc.ontrack = (e) => {
      audioEl = new Audio();
      audioEl.srcObject = e.streams[0];
      audioEl.autoplay = true;
      audioEl.play().catch(err => console.error('audio play:', err));
    };

    pc.onconnectionstatechange = () => {
      console.log('WebRTC state:', pc.connectionState);
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        if (audioPc === pc) {
          audioPc = null;
          $('#audioButton').removeClass("icons-selected");
        }
      }
    };

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    // 相对当前页面，最终命中 /app/kasm-lxqt/audio/offer
    const res = await fetch('audio/offer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: pc.localDescription.sdp,
    });

    if (!res.ok) {
      throw new Error('audio/offer ' + res.status + ': ' + await res.text());
    }

    await pc.setRemoteDescription({
      type: 'answer',
      sdp: await res.text(),
    });
  } catch (err) {
    console.error('audio error:', err);
    if (audioPc) { audioPc.close(); audioPc = null; }
    $('#audioButton').removeClass("icons-selected");
  }
}

//// 麦克风：后端目前没有上行 track，先禁用 ////
function mic() {
  console.warn('mic not implemented for WebRTC backend yet');
}

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
      if (socket.readyState === WebSocket.OPEN) {
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
