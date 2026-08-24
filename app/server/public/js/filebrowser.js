var host = window.location.hostname;
var port = window.location.port;
var protocol = window.location.protocol;
var basePath = window.location.pathname.replace(/\/files\/?$/, '').replace(/\/+$/, '');
var wsProtocol = protocol === 'https:' ? 'wss://' : 'ws://';
var socket = null;
var connected = false;
var fmRoot = null;
var pendingDownload = null;
var uploadQueue = [];
var uploading = false;

function connect() {
  socket = new WebSocket(wsProtocol + host + ':' + port + basePath + '/files/ws');
  socket.binaryType = 'arraybuffer';
  socket.onopen = function() {
    connected = true;
    send({type: 'open'});
  };
  socket.onmessage = handleMessage;
  socket.onclose = function() {
    connected = false;
    $('#filebrowser').empty();
    $('#filebrowser').append($('<div>').text('Connection lost, retrying...'));
    setTimeout(connect, 3000);
  };
}

function send(message) {
  if (connected) {
    socket.send(JSON.stringify(message));
  }
}

function handleMessage(event) {
  if (typeof event.data === 'string') {
    var message = JSON.parse(event.data);
    if (message.type === 'renderfiles') {
      renderFiles(message);
    } else if (message.type === 'download') {
      pendingDownload = message.name;
    } else if (message.type === 'upload-ack') {
      uploading = false;
      processUploadQueue();
    } else if (message.type === 'error') {
      alert(message.message);
      uploading = false;
      processUploadQueue();
    }
    return;
  }

  if (pendingDownload) {
    saveBlob(event.data, pendingDownload);
    pendingDownload = null;
  }
}

connect();

// Get file list
function getFiles(directory) {
  if (!directory || directory === '/') return;
  showLoading();
  send({type: 'getfiles', directory: directory});
}

// Render file list
function renderFiles(message) {
  var dirs = message.dirs || [];
  var files = message.files || [];
  var directory = message.directory;
  fmRoot = message.root;
  var table = $('<table>').addClass('fileTable');
  var header = $('<tr>');
  for (var name of ['Name', 'Type', 'Delete (NO WARNING)']) {
    header.append($('<th>').text(name));
  }
  table.append(header);

  if (directory !== fmRoot) {
    var parentDirectory = directory.slice(0, directory.lastIndexOf('/')) || '/';
    var parentRow = $('<tr>');
    parentRow.append($('<td>').addClass('directory').text('..').on('click', function() {
      getFiles(parentDirectory);
    }));
    parentRow.append($('<td>').text('Parent'));
    parentRow.append($('<td>'));
    table.append(parentRow);
  }

  for (var dir of dirs) {
    var dirPath = directory + '/' + dir;
    var dirRow = $('<tr>');
    dirRow.append($('<td>').addClass('directory').text(dir).on('click', function() {
      getFiles(dirPath);
    }));
    dirRow.append($('<td>').text('Dir'));
    dirRow.append($('<td>').append($('<button>').addClass('deleteButton').text('Delete').on('click', function() {
      deleter(dirPath);
    })));
    table.append(dirRow);
  }

  for (var file of files) {
    var filePath = directory + '/' + file;
    var fileRow = $('<tr>');
    fileRow.append($('<td>').addClass('file').text(file).on('click', function() {
      downloadFile(filePath);
    }));
    fileRow.append($('<td>').text('File'));
    fileRow.append($('<td>').append($('<button>').addClass('deleteButton').text('Delete').on('click', function() {
      deleter(filePath);
    })));
    table.append(fileRow);
  }

  $('#filebrowser').empty();
  $('#filebrowser').data('directory', directory);
  $('#filebrowser').append($('<div>').text(directory));
  $('#filebrowser').append(table);
}

// Download a file
function downloadFile(file) {
  showLoading();
  send({type: 'download', file: file});
}

// Send buffer to download blob
function saveBlob(data, fileName) {
  var blob = new Blob([data], { type: "application/octet-stream" });
  var url = window.URL || window.webkitURL;
  var link = url.createObjectURL(blob);
  var a = $("<a />");
  a.attr("download", fileName);
  a.attr("href", link);
  $("body").append(a);
  a[0].click();
  $("body").remove(a);
  setTimeout(function() { url.revokeObjectURL(link); }, 1000);
}

// Upload files to current directory
async function upload(input) {
  var fileList = Array.from(input.files || []);
  var directory = $('#filebrowser').data('directory');
  var directoryUp = directory === '/' ? '' : directory;
  for (var file of fileList) {
    uploadQueue.push({file: file, path: directoryUp + '/' + file.name});
  }
  processUploadQueue();
}

function processUploadQueue() {
  if (uploading || uploadQueue.length === 0) return;
  uploading = true;
  var item = uploadQueue.shift();
  var directory = $('#filebrowser').data('directory');
  showLoading();

  readFileBuffer(item.file).then(function(data) {
    if (data.byteLength >= 200000000) {
      $('#filebrowser').empty();
      $('#filebrowser').append($('<div>').text('File too big ' + item.file.name));
      uploading = false;
      processUploadQueue();
      return;
    }
    $('#filebrowser').append($('<div>').text('Uploading ' + item.file.name));
    send({type: 'upload', directory: directory, path: item.path, render: uploadQueue.length === 0});
    socket.send(data);
  }).catch(function(error) {
    alert('Upload failed: ' + error.message);
    uploading = false;
    processUploadQueue();
  });
}

function readFileBuffer(file) {
  return new Promise(function(resolve, reject) {
    var reader = new FileReader();
    reader.onload = function() { resolve(reader.result); };
    reader.onerror = function() { reject(reader.error || new Error('read failed')); };
    reader.readAsArrayBuffer(file);
  });
}

// Delete file/folder
function deleter(item) {
  var directory = $('#filebrowser').data('directory');
  showLoading();
  send({type: 'delete', item: item, directory: directory});
}

// Delete file/folder
function createFolder() {
  var directory = $('#filebrowser').data('directory');
  var directoryUp = directory === '/' ? '' : directory;
  var folderName = $('#folderName').val();
  $('#folderName').val('');
  if ((folderName.length == 0) || (folderName.includes('/'))) {
    alert('Bad or Null Directory Name');
    return '';
  }
  showLoading();
  send({type: 'createfolder', dir: directoryUp + '/' + folderName, directory: directory});
}

function showLoading() {
  $('#filebrowser').empty();
  $('#filebrowser').append($('<div>').attr('id','loading'));
}

// Handle drag and drop
async function dropFiles(ev) {
  ev.preventDefault();
  showLoading();
  $('#dropzone').css({'visibility':'hidden','opacity':0});
  var directory = $('#filebrowser').data('directory');
  var directoryUp = directory === '/' ? '' : directory;
  var entries = await getAllFileEntries(ev.dataTransfer.items);
  for (var entry of entries) {
    var fullPath = entry.fullPath.replace(/^\/+/, '');
    var file = await getFileFromEntry(entry);
    uploadQueue.push({file: file, path: directoryUp + '/' + fullPath});
  }
  processUploadQueue();
}

function getFileFromEntry(entry) {
  return new Promise(function(resolve, reject) {
    entry.file(resolve, reject);
  });
}

// Drop handler function to get all files
async function getAllFileEntries(dataTransferItemList) {
  var fileEntries = [];
  // Use BFS to traverse entire directory/file structure
  var queue = [];
  // Unfortunately dataTransferItemList is not iterable i.e. no forEach
  for (var i = 0; i < dataTransferItemList.length; i++) {
    queue.push(dataTransferItemList[i].webkitGetAsEntry());
  }
  while (queue.length > 0) {
    var entry = queue.shift();
    if (entry.isFile) {
      fileEntries.push(entry);
    } else if (entry.isDirectory) {
      var reader = entry.createReader();
      queue.push(...await readAllDirectoryEntries(reader));
    }
  }
  return fileEntries;
}
// Get all the entries (files or sub-directories) in a directory by calling readEntries until it returns empty array
async function readAllDirectoryEntries(directoryReader) {
  var entries = [];
  var readEntries = await readEntriesPromise(directoryReader);
  while (readEntries.length > 0) {
    entries.push(...readEntries);
    readEntries = await readEntriesPromise(directoryReader);
  }
  return entries;
}
// Wrap readEntries in a promise to make working with readEntries easier
async function readEntriesPromise(directoryReader) {
  try {
    return await new Promise((resolve, reject) => {
      directoryReader.readEntries(resolve, reject);
    });
  } catch (err) {
    console.log(err);
  }
}

var lastTarget;
// Change style when hover files
window.addEventListener('dragenter', function(ev) {
  lastTarget = ev.target;
  $('#dropzone').css({'visibility':'','opacity':1});
});

// Change style when leave hover files
window.addEventListener("dragleave", function(ev) {
  if(ev.target == lastTarget || ev.target == document) {
    $('#dropzone').css({'visibility':'hidden','opacity':0});
  }
});

// Disabled default drag and drop
function allowDrop(ev) {
  ev.preventDefault();
}
