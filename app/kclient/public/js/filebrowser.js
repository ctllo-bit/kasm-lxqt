// ------------------------------------------------------------
// Socket.IO
// ------------------------------------------------------------
const host = window.location.hostname;
const port = window.location.port;
const protocol = window.location.protocol;
const base = window.location.pathname.replace(/\/files.*$/, '/');

const socket = io(`${protocol}//${host}:${port}`, {
  path: `${base}files/socket.io/`
});

// ------------------------------------------------------------
// DOM 工具
// ------------------------------------------------------------
const q = (sel) => document.querySelector(sel);
const filebrowserEl = q('#filebrowser');
const folderNameInput = q('#folderName');
const dropzoneEl = q('#dropzone');

function setLoading() {
  filebrowserEl.replaceChildren();
  const div = document.createElement('div');
  div.id = 'loading';
  filebrowserEl.append(div);
}

// ------------------------------------------------------------
// 连接与打开默认目录
// ------------------------------------------------------------
socket.on('connect', () => {
  setLoading();
  socket.emit('open', '');
});

// ------------------------------------------------------------
// 获取文件列表
// ------------------------------------------------------------
function getFiles(directory) {
  directory = directory.replace('//', '/').replace('|', "'");
  if (directory !== '/' && directory.endsWith('/')) {
    directory = directory.slice(0, -1);
  }
  setLoading();
  socket.emit('getfiles', directory);
}

// ------------------------------------------------------------
// 渲染文件列表
// ------------------------------------------------------------
function renderFiles(data) {
  const dirs = data[0];
  const files = data[1];
  const directory = data[2];

  const baseName = directory.split('/').slice(-1)[0];
  const parentFolder = directory.replace(baseName, '');

  filebrowserEl.replaceChildren();
  filebrowserEl.dataset.directory = directory;

  // 当前路径
  const title = document.createElement('div');
  title.textContent = directory;
  filebrowserEl.append(title);

  // 表格
  const table = document.createElement('table');
  table.className = 'fileTable';

  // 表头
  const thead = document.createElement('tr');
  for (const name of ['Name', 'Type', 'Delete (NO WARNING)']) {
    const th = document.createElement('th');
    th.textContent = name;
    thead.append(th);
  }
  table.append(thead);

  // 父目录行
  const parentRow = document.createElement('tr');
  const parentCell = document.createElement('td');
  parentCell.className = 'directory';
  parentCell.textContent = '..';
  parentCell.addEventListener('click', () => getFiles(parentFolder));
  parentRow.append(parentCell);
  parentRow.append(Object.assign(document.createElement('td'), { textContent: 'Parent' }));
  parentRow.append(document.createElement('td'));
  table.append(parentRow);

  // 目录行
  for (const dir of dirs) {
    const tr = document.createElement('tr');

    const link = document.createElement('td');
    link.className = 'directory';
    link.textContent = dir;
    link.addEventListener('click', () => getFiles(`${directory}/${dir}`));

    const type = document.createElement('td');
    type.textContent = 'Dir';

    const delTd = document.createElement('td');
    const delBtn = document.createElement('button');
    delBtn.type = 'button';
    delBtn.className = 'deleteButton';
    delBtn.textContent = 'Delete';
    delBtn.addEventListener('click', () => deleter(`${directory}/${dir}`));
    delTd.append(delBtn);

    tr.append(link, type, delTd);
    table.append(tr);
  }

  // 文件行
  for (const file of files) {
    const tr = document.createElement('tr');

    const link = document.createElement('td');
    link.className = 'file';
    link.textContent = file;
    link.addEventListener('click', () => downloadFile(`${directory}/${file}`));

    const type = document.createElement('td');
    type.textContent = 'File';

    const delTd = document.createElement('td');
    const delBtn = document.createElement('button');
    delBtn.type = 'button';
    delBtn.className = 'deleteButton';
    delBtn.textContent = 'Delete';
    delBtn.addEventListener('click', () => deleter(`${directory}/${file}`));
    delTd.append(delBtn);

    tr.append(link, type, delTd);
    table.append(tr);
  }

  filebrowserEl.append(table);
}

// ------------------------------------------------------------
// 下载
// ------------------------------------------------------------
function downloadFile(file) {
  file = file.replace('|', "'");
  socket.emit('downloadfile', file);
}

function sendFile(res) {
  const data = res[0];
  const fileName = res[1];

  const blob = new Blob([data], { type: 'application/octet-stream' });
  const url = window.URL || window.webkitURL;
  const link = url.createObjectURL(blob);

  const a = document.createElement('a');
  a.download = fileName;
  a.href = link;
  document.body.append(a);
  a.click();
  a.remove();

  url.revokeObjectURL(link);
}

// ------------------------------------------------------------
// 上传
// ------------------------------------------------------------
async function upload(input) {
  const directory = filebrowserEl.dataset.directory || '/';
  const directoryUp = directory === '/' ? '' : directory;

  if (!input.files || !input.files[0]) return;

  setLoading();

  const files = Array.from(input.files);
  for (const file of files) {
    const data = await file.arrayBuffer();

    if (data.byteLength < 200000000) {
      const info = document.createElement('div');
      info.textContent = 'Uploading ' + file.name;
      filebrowserEl.append(info);

      const isLast = file === files[files.length - 1];
      socket.emit('uploadfile', [directory, `${directoryUp}/${file.name}`, data, isLast]);
    } else {
      const info = document.createElement('div');
      info.textContent = 'File too big ' + file.name;
      filebrowserEl.append(info);

      await new Promise((resolve) => setTimeout(resolve, 2000));
      socket.emit('getfiles', directory);
    }
  }
}

// ------------------------------------------------------------
// 删除 / 新建文件夹
// ------------------------------------------------------------
function deleter(item) {
  const directory = filebrowserEl.dataset.directory;
  setLoading();
  socket.emit('deletefiles', [item, directory]);
}

function createFolder() {
  const directory = filebrowserEl.dataset.directory || '/';
  const directoryUp = directory === '/' ? '' : directory;
  const folderName = folderNameInput.value;
  folderNameInput.value = '';

  if (!folderName || folderName.includes('/')) {
    alert('Bad or Null Directory Name');
    return;
  }

  setLoading();
  socket.emit('createfolder', [`${directoryUp}/${folderName}`, directory]);
}

// ------------------------------------------------------------
// 拖拽上传
// ------------------------------------------------------------
async function dropFiles(ev) {
  ev.preventDefault();
  setLoading();
  dropzoneEl.style.visibility = 'hidden';
  dropzoneEl.style.opacity = 0;

  const directory = filebrowserEl.dataset.directory || '/';
  const directoryUp = directory === '/' ? '' : directory;

  const items = await getAllFileEntries(ev.dataTransfer.items);
  const files = await Promise.all(items.map((it) => it.file()));

  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    const fullPath = items[i].fullPath;
    const data = await file.arrayBuffer();

    if (data.byteLength < 200000000) {
      const info = document.createElement('div');
      info.textContent = 'Uploading ' + file.name;
      filebrowserEl.append(info);

      const isLast = i === files.length - 1;
      socket.emit('uploadfile', [directory, directoryUp + fullPath, data, isLast]);
    } else {
      const info = document.createElement('div');
      info.textContent = 'File too big ' + file.name;
      filebrowserEl.append(info);

      await new Promise((resolve) => setTimeout(resolve, 2000));
      socket.emit('getfiles', directory);
    }
  }
}

// BFS 遍历拖进来的文件夹
function getAllFileEntries(dataTransferItemList) {
  const queue = [];
  for (let i = 0; i < dataTransferItemList.length; i++) {
    const entry = dataTransferItemList[i].webkitGetAsEntry();
    if (entry) queue.push(entry);
  }

  const fileEntries = [];

  async function walk() {
    while (queue.length > 0) {
      const entry = queue.shift();
      if (entry.isFile) {
        fileEntries.push(entry);
      } else if (entry.isDirectory) {
        const reader = entry.createReader();
        queue.push(...(await readAllDirectoryEntries(reader)));
      }
    }
  }

  return walk().then(() => fileEntries);
}

async function readAllDirectoryEntries(directoryReader) {
  const entries = [];
  let batch = await readEntriesPromise(directoryReader);
  while (batch.length > 0) {
    entries.push(...batch);
    batch = await readEntriesPromise(directoryReader);
  }
  return entries;
}

function readEntriesPromise(directoryReader) {
  return new Promise((resolve, reject) => {
    directoryReader.readEntries(resolve, reject);
  });
}

function allowDrop(ev) {
  ev.preventDefault();
}

// ------------------------------------------------------------
// 拖拽悬停样式
// ------------------------------------------------------------
let lastTarget = null;

window.addEventListener('dragenter', (ev) => {
  lastTarget = ev.target;
  dropzoneEl.style.visibility = '';
  dropzoneEl.style.opacity = 1;
});

window.addEventListener('dragleave', (ev) => {
  if (ev.target === lastTarget || ev.target === document) {
    dropzoneEl.style.visibility = 'hidden';
    dropzoneEl.style.opacity = 0;
  }
});

// ------------------------------------------------------------
// DOM 事件绑定
// ------------------------------------------------------------
document.addEventListener('DOMContentLoaded', () => {
  q('#createFolderBtn').addEventListener('click', createFolder);
  q('#uploadBtn').addEventListener('click', () => q('#uploadInput').click());
  q('#uploadInput').addEventListener('change', (e) => upload(e.target));

  dropzoneEl.addEventListener('dragover', allowDrop);
  dropzoneEl.addEventListener('drop', dropFiles);
});

// ------------------------------------------------------------
// Socket.IO 入站事件
// ------------------------------------------------------------
socket.on('renderfiles', renderFiles);
socket.on('sendfile', sendFile);