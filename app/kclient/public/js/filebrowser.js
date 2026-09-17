// Native WebSocket protocol used by the Go file service.
var socketProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
var socket = new WebSocket(socketProtocol + '//' + window.location.host + window.location.pathname + '/ws');
socket.binaryType = 'arraybuffer';
var pendingDownloadName = '';

function send(type, fields) {
  if (socket.readyState !== WebSocket.OPEN) { console.error('file WebSocket is not connected'); return false; }
  socket.send(JSON.stringify(Object.assign({ type: type }, fields || {})));
  return true;
}
socket.addEventListener('open', function() {
  $('#filebrowser').empty().append($('<div>').attr('id', 'loading'));
  send('open');
});
socket.addEventListener('message', function(event) {
  if (event.data instanceof ArrayBuffer) {
    if (pendingDownloadName) saveDownload(event.data, pendingDownloadName);
    pendingDownloadName = '';
    return;
  }
  try {
    var message = JSON.parse(event.data);
    if (message.type === 'renderfiles') renderFiles(message);
    else if (message.type === 'download') pendingDownloadName = message.name;
    else if (message.type === 'error') { $('#filebrowser').empty().append($('<div>').text('Error: ' + message.error)); console.error(message.error); }
  } catch (error) { console.error('invalid file service response', error); }
});

function cleanPath(value) {
  var parts = [];
  value.split('/').forEach(function(part) { if (!part || part === '.') return; if (part === '..') parts.pop(); else parts.push(part); });
  return '/' + parts.join('/');
}
function childPath(directory, name) { return cleanPath(directory + '/' + name); }
function parentPath(directory) { return cleanPath(directory + '/..'); }
function getFiles(directory) {
  $('#filebrowser').empty().append($('<div>').attr('id', 'loading'));
  send('getfiles', { directory: cleanPath(directory) });
}
function renderFiles(data) {
  var directory = data.directory, table = $('<table>').addClass('fileTable'), header = $('<tr>');
  ['Name', 'Type', 'Delete (NO WARNING)'].forEach(function(name) { header.append($('<th>').text(name)); });
  table.append(header);
  table.append($('<tr>').append($('<td>').addClass('directory').text('..').on('click', function() { getFiles(parentPath(directory)); }), $('<td>').text('Parent'), $('<td>')));
  $('#filebrowser').empty().data('directory', directory).append($('<div>').text(directory), table);
  data.dirs.forEach(function(name) { addRow(table, directory, name, 'Dir'); });
  data.files.forEach(function(name) { addRow(table, directory, name, 'File'); });
}
function addRow(table, directory, name, type) {
  var path = childPath(directory, name), row = $('<tr>');
  var nameCell = $('<td>').addClass(type === 'Dir' ? 'directory' : 'file').text(name);
  nameCell.on('click', function() { if (type === 'Dir') getFiles(path); else downloadFile(path); });
  row.append(nameCell, $('<td>').text(type), $('<td>').append($('<button>').addClass('deleteButton').text('Delete').on('click', function() { deleter(path); })));
  table.append(row);
}
function downloadFile(path) { send('download', { path: path }); }
function saveDownload(data, fileName) {
  var link = URL.createObjectURL(new Blob([data], { type: 'application/octet-stream' }));
  var anchor = $('<a />').attr({ download: fileName, href: link }).appendTo('body');
  anchor[0].click(); anchor.remove(); URL.revokeObjectURL(link);
}
async function upload(input) {
  var entries = Array.from(input.files || []).map(function(file) { return { file: file, path: file.name }; });
  await uploadFiles(entries); input.value = '';
}
async function uploadFiles(entries) {
  var directory = $('#filebrowser').data('directory'); if (!directory || !entries.length) return;
  $('#filebrowser').empty().append($('<div>').attr('id', 'loading'));
  for (var entry of entries) {
    if (entry.file.size >= 200000000) { console.warn('File too big', entry.file.name); continue; }
    $('#filebrowser').append($('<div>').text('Uploading ' + entry.file.name));
    if (!send('upload', { path: childPath(directory, entry.path) })) return;
    socket.send(await entry.file.arrayBuffer());
  }
}
function deleter(path) {
  var directory = $('#filebrowser').data('directory');
  $('#filebrowser').empty().append($('<div>').attr('id', 'loading'));
  send('delete', { path: path, directory: directory });
}
function createFolder() {
  var directory = $('#filebrowser').data('directory'), name = $('#folderName').val(); $('#folderName').val('');
  if (!name || name.includes('/') || name === '.' || name === '..') { alert('Bad or Null Directory Name'); return; }
  $('#filebrowser').empty().append($('<div>').attr('id', 'loading'));
  send('mkdir', { path: childPath(directory, name), directory: directory });
}
async function dropFiles(event) {
  event.preventDefault(); $('#dropzone').css({ visibility:'hidden', opacity:0 });
  var items = await getAllFileEntries(event.dataTransfer.items), entries = [];
  for (var item of items) { entries.push({ file: await new Promise(function(resolve, reject) { item.file(resolve, reject); }), path: item.fullPath }); }
  await uploadFiles(entries);
}
async function getAllFileEntries(itemList) {
  var files = [], queue = [];
  for (var i = 0; i < itemList.length; i++) { var entry = itemList[i].webkitGetAsEntry(); if (entry) queue.push(entry); }
  while (queue.length) { var current = queue.shift(); if (current.isFile) files.push(current); else if (current.isDirectory) queue.push.apply(queue, await readAllDirectoryEntries(current.createReader())); }
  return files;
}
async function readAllDirectoryEntries(reader) {
  var entries = [], batch;
  do { batch = await new Promise(function(resolve, reject) { reader.readEntries(resolve, reject); }); entries.push.apply(entries, batch); } while (batch.length);
  return entries;
}
var lastTarget;
window.addEventListener('dragenter', function(event) { lastTarget = event.target; $('#dropzone').css({ visibility:'', opacity:1 }); });
window.addEventListener('dragleave', function(event) { if (event.target === lastTarget || event.target === document) $('#dropzone').css({ visibility:'hidden', opacity:0 }); });
function allowDrop(event) { event.preventDefault(); }
