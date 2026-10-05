'use strict';

/* ================= 状态与工具 ================= */

const $ = s => document.querySelector(s);
const $$ = s => [...document.querySelectorAll(s)];

const S = {
  entries: [],            // 传输流条目
  ws: null, wsOk: false,
  tab: 'stream',
  cwd: '/',               // 文件浏览当前目录（虚拟路径，如 /1/Movies）
  rows: [],               // 当前展示的文件行（目录或搜索结果）
  searchMode: false,
  favorites: [],          // 服务端收藏
  recent: JSON.parse(localStorage.getItem('trans.recent') || '[]'),
  sel: { on: false, paths: new Set() },  // 文件页多选
  media: null,            // 相册扫描结果
  images: [], imgIndex: 0,
  playerQueue: [], playerIndex: 0,       // 连播队列
  isPC: ['127.0.0.1', 'localhost'].includes(location.hostname),
  net: null, settings: null,
  uploads: [],
};

const VIDEO = ['mp4', 'webm', 'm4v', 'mov', 'ogv', 'mkv', 'avi'];
const AUDIO = ['mp3', 'm4a', 'aac', 'wav', 'ogg', 'flac', 'opus'];
const IMAGE = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'ico', 'avif', 'heic'];
const TEXT  = ['txt', 'md', 'log', 'json', 'xml', 'js', 'ts', 'css', 'html', 'py', 'go', 'java', 'c', 'h', 'cpp', 'cs', 'sh', 'bat', 'ps1', 'yml', 'yaml', 'toml', 'ini', 'conf', 'csv'];
const ICONS = { video: '🎬', audio: '🎵', image: '🖼', text: '📄', other: '📦', dir: '📁' };

const ext = n => { const i = n.lastIndexOf('.'); return i < 0 ? '' : n.slice(i + 1).toLowerCase(); };
function kindOf(name) {
  const e = ext(name);
  if (VIDEO.includes(e)) return 'video';
  if (AUDIO.includes(e)) return 'audio';
  if (IMAGE.includes(e)) return 'image';
  if (TEXT.includes(e)) return 'text';
  return 'other';
}
function fmtSize(n) {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
  return (n / 1073741824).toFixed(2) + ' GB';
}
function fmtDur(sec) {
  if (!sec || sec < 0) return '';
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
  return (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(s).padStart(2, '0');
}
function fmtTime(ts) {
  const d = new Date(ts), now = new Date();
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  return d.toDateString() === now.toDateString() ? hm : `${d.getMonth() + 1}-${d.getDate()} ${hm}`;
}
function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

let toastTimer;
function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add('hidden'), 2000);
}

async function api(path, opts = {}) {
  const r = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...opts });
  let data = {};
  try { data = await r.json(); } catch {}
  if (!r.ok) {
    const err = new Error(data.error || r.statusText);
    err.status = r.status;
    err.data = data;
    throw err;
  }
  return data;
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.cssText = 'position:fixed;opacity:0;left:-999px';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch {}
  ta.remove();
  return ok;
}
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); return true; } catch {}
  }
  return fallbackCopy(text);
}

/* 文件 URL：传输流条目 / 文件浏览路径 / 对应的转码地址 */
const fileUrl = e => `/f/${e.id}/${encodeURIComponent(e.name)}`;
const dlUrl = e => fileUrl(e) + '?dl=1';
function fsFileUrl(fullRel) {
  return '/b/' + fullRel.split('/').filter(Boolean).map(encodeURIComponent).join('/');
}
const tcEntryUrl = e => `/t/id/${e.id}`;
const tcFsUrl = fullRel => '/t/v/' + fullRel.split('/').filter(Boolean).map(encodeURIComponent).join('/');
const joinPath = (dir, name) => dir === '/' ? '/' + name : dir + '/' + name;

function show(id) { $('#' + id).classList.remove('hidden'); }
function hide(id) {
  $('#' + id).classList.add('hidden');
  if (id === 'playerOverlay') { const v = $('#player'); v.pause(); v.removeAttribute('src'); v.load(); }
  if (id === 'imageOverlay') $('#imgView').removeAttribute('src');
}
function downloadUrl(url, name) {
  const a = document.createElement('a');
  a.href = url;
  a.download = name || '';
  document.body.appendChild(a);
  a.click();
  a.remove();
  toast('开始下载');
}

/* ================= 启动（含 PIN 门禁） ================= */

async function bootstrap() {
  try {
    const ping = await api('/api/ping');
    if (ping.pinRequired) {
      try { await api('/api/settings'); } // 已带 cookie 则直接过
      catch (e) {
        if (e.status === 401) { show('pinOverlay'); $('#pinInput').focus(); return; }
        throw e;
      }
    }
    const [settings, net] = await Promise.all([api('/api/settings'), api('/api/net')]);
    S.settings = settings;
    S.net = net;
    S.favorites = settings.favorites || [];
    if (ping.paused) showPause(true);
    renderConsole();
    const stream = await api('/api/stream');
    S.entries = stream.entries || [];
    renderStream();
    $('#connOverlay').classList.add('hidden');
    connectWS();
    loadDir('/');
    maybeA2HS();
    prefetchDurations();
  } catch {
    $('#connOverlay').classList.remove('hidden');
  }
}
$('#retryBtn').onclick = bootstrap;
$('#bannerRetry').onclick = () => location.reload();

$('#pinSubmit').onclick = async () => {
  const pin = $('#pinInput').value.trim();
  if (!pin) return;
  try {
    await api('/api/auth', { method: 'POST', body: JSON.stringify({ pin }) });
    hide('pinOverlay');
    $('#pinInput').value = '';
    bootstrap();
  } catch { toast('PIN 不正确'); }
};
$('#pinInput').addEventListener('keydown', ev => { if (ev.key === 'Enter') $('#pinSubmit').click(); });

/* 服务探活 */
setInterval(async () => {
  try {
    await api('/api/ping');
  } catch {
    $('#banner').classList.remove('hidden');
  }
}, 15000);

/* 添加到主屏幕引导（手机，仅一次） */
function maybeA2HS() {
  if (S.isPC) return;
  const standalone = matchMedia('(display-mode: standalone)').matches || navigator.standalone;
  if (standalone || localStorage.getItem('trans.a2hs')) return;
  $('#a2hsCard').classList.remove('hidden');
}
$('#a2hsClose').onclick = () => { $('#a2hsCard').classList.add('hidden'); localStorage.setItem('trans.a2hs', '1'); };

/* ================= Tab ================= */

$$('.tabs button').forEach(b => b.onclick = () => switchTab(b.dataset.tab));
function switchTab(tab) {
  S.tab = tab;
  $$('.tabs button').forEach(x => x.classList.toggle('active', x.dataset.tab === tab));
  $('#view-stream').classList.toggle('hidden', tab !== 'stream');
  $('#view-files').classList.toggle('hidden', tab !== 'files');
  $('#view-media').classList.toggle('hidden', tab !== 'media');
  $('#tabTitle').textContent = tab === 'stream' ? '传输' : tab === 'files' ? '文件' : '相册';
  if (tab === 'media' && !S.media) loadMedia();
  if (tab !== 'files') exitSelectMode();
  setTyping();
}

/* ================= WebSocket 同步 ================= */

function connectWS() {
  if (S.ws) { try { S.ws.onclose = null; S.ws.close(); } catch {} }
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  S.ws = ws;
  ws.onopen = () => {
    S.wsOk = true; setDot();
    $('#banner').classList.add('hidden');
    fullSync(); // 重连一律全量拉平
  };
  ws.onmessage = ev => {
    let m; try { m = JSON.parse(ev.data); } catch { return; }
    if (m.type === 'add' && m.entry) {
      if (!S.entries.some(e => e.id === m.entry.id)) S.entries.push(m.entry);
      renderStream();
      prefetchDurations();
    } else if (m.type === 'remove') {
      S.entries = S.entries.filter(e => e.id !== m.id);
      renderStream();
    } else if (m.type === 'bulk-remove') {
      fullSync();
    } else if (m.type === 'pause') {
      showPause(!!m.paused);
    } else if (m.type === 'clip-files') {
      // PC 主页轻提示：检测到复制了文件，一键确认上架（不自动）
      if (S.isPC) {
        const names = (m.names || []).join('、');
        $('#clipBannerText').textContent = `检测到复制了 ${m.count} 个文件${names ? '：' + names : ''}`;
        show('clipBanner');
      }
    } else if (m.type === 'clip-pause') {
      updateClipPauseBtn(m.until);
    }
  };
  ws.onclose = () => {
    S.wsOk = false; setDot();
    $('#banner').classList.remove('hidden');
    setTimeout(connectWS, 2000);
  };
}
async function fullSync() {
  try {
    const d = await api('/api/stream');
    S.entries = d.entries || [];
    renderStream();
  } catch {}
}
function setDot() { $('#connDot').classList.toggle('ok', S.wsOk); }

$('#clipStageBtn').onclick = async () => {
  try {
    const d = await api('/api/internal/clip-stage', { method: 'POST' });
    toast(`已上架 ${d.added} 个文件`);
  } catch { toast('上架失败'); }
  hide('clipBanner');
};
$('#clipIgnoreBtn').onclick = () => hide('clipBanner');

let pauseTimer;
function showPause(p) {
  $('#pauseOverlay').classList.toggle('hidden', !p);
  clearInterval(pauseTimer);
  if (p) {
    pauseTimer = setInterval(async () => {
      try {
        const d = await api('/api/ping');
        if (!d.paused) { clearInterval(pauseTimer); showPause(false); fullSync(); }
      } catch {}
    }, 3000);
  }
}

/* ================= 传输流 ================= */

function renderStream() {
  const list = [...S.entries].sort((a, b) => b.created - a.created);
  $('#streamEmpty').classList.toggle('hidden', list.length > 0);
  $('#streamList').innerHTML = list.map(e => {
    if (e.kind === 'text') {
      const short = e.text.length > 400 ? e.text.slice(0, 400) + '…' : e.text;
      return `<div class="card text" data-id="${e.id}">
        <div class="ctext" title="点击复制">${esc(short)}</div>
        <div class="cmeta"><span>${fmtTime(e.created)} · 文本${e.origin === 'clip' ? ' · 电脑复制' : ''}</span>
          <span class="acts">
            <button class="mini" data-act="copy">复制</button>
            <button class="mini" data-act="menu">⋯</button>
          </span></div>
      </div>`;
    }
    const badge = e.storage === 'ref'
      ? '<i class="badge ref">引用</i>'
      : '<i class="badge copy">副本</i>';
    const missing = e.missing ? '<span style="color:var(--danger);font-size:12px">原文件已不在</span>' : '';
    const k = kindOf(e.name);
    const icon = k === 'image' && !e.missing
      ? `<img class="thumb" loading="lazy" src="/api/thumb?id=${e.id}&w=160" alt="">`
      : `<span class="cicon">${ICONS[k] || '📦'}</span>`;
    const dur = k === 'video' ? `<i class="durbadge" data-dur-id="${e.id}"></i>` : '';
    return `<div class="card file ${e.missing ? 'missing' : ''}" data-id="${e.id}">
      ${icon}
      <div class="cbody">
        <div class="cname">${esc(e.name)} ${badge}</div>
        <div class="cmeta"><span>${fmtSize(e.size)} ${dur}</span><span>${fmtTime(e.created)} ${missing}</span></div>
      </div>
      ${e.missing ? '' : '<button class="copen" data-act="open">打开</button>'}
      <button class="mini" data-act="menu">⋯</button>
    </div>`;
  }).join('');
}

/* 视频时长徽标：懒取（只处理最近 20 个视频条目） */
async function prefetchDurations() {
  if (!S.settings || !S.settings.ffmpeg) return; // 无 ffprobe 时交给播放器自己显示
  const vids = S.entries.filter(e => e.kind === 'file' && !e.missing && kindOf(e.name) === 'video').slice(0, 20);
  for (const e of vids) {
    const el = document.querySelector(`[data-dur-id="${e.id}"]`);
    if (!el || el.textContent) continue;
    api(`/api/meta?id=${e.id}`).then(d => { if (d.duration) el.textContent = fmtDur(d.duration); }).catch(() => {});
  }
}

$('#streamList').addEventListener('click', ev => {
  const card = ev.target.closest('.card');
  if (!card) return;
  const e = S.entries.find(x => x.id === card.dataset.id);
  if (!e) return;
  const act = ev.target.dataset.act;
  if (act === 'menu') return streamMenu(e);
  if (e.kind === 'text') return copyTextFlow(e);
  if (act === 'open') return openEntry(e);
  if (!act) openEntry(e);
});

async function copyTextFlow(e) {
  const ok = await copyText(e.text);
  toast(ok ? (S.isPC ? '已复制到电脑剪贴板' : '已复制') : '复制失败');
}

function openEntry(e) {
  const k = kindOf(e.name);
  if (k === 'video' || k === 'audio') {
    const vids = S.entries.filter(x => x.kind === 'file' && !x.missing && ['video', 'audio'].includes(kindOf(x.name)))
      .map(x => ({ name: x.name, url: fileUrl(x), tc: tcEntryUrl(x) }));
    openPlayer(e.name, fileUrl(e), { queue: vids, tc: tcEntryUrl(e), subId: e.id });
  } else if (k === 'image') {
    const imgs = S.entries.filter(x => x.kind === 'file' && !x.missing && kindOf(x.name) === 'image');
    openImageList(imgs.map(x => ({ name: x.name, url: fileUrl(x) })), Math.max(0, imgs.findIndex(x => x.id === e.id)));
  } else if (k === 'text') {
    openText(e.name, fileUrl(e), e.size);
  } else {
    downloadUrl(dlUrl(e), e.name);
  }
}

function streamMenu(e) {
  const items = [];
  if (e.kind === 'text') {
    items.push({ label: '📋 复制', fn: () => copyTextFlow(e) });
    if (S.isPC) items.push({ label: '🖥 写入系统剪贴板', fn: async () => {
      await api('/api/clipboard', { method: 'POST', body: JSON.stringify({ text: e.text }) });
      toast('已写入系统剪贴板');
    }});
  } else {
    if (!e.missing) items.push({ label: '👁 打开 / 播放', fn: () => openEntry(e) });
    items.push({ label: '⬇ 下载', fn: () => downloadUrl(dlUrl(e), e.name) });
    items.push({ label: '🔗 复制链接', fn: async () => {
      const ok = await copyText(location.origin + fileUrl(e));
      toast(ok ? '已复制链接' : '复制失败');
    }});
    items.push({ label: '📱 生成二维码', fn: () => {
      $('#qrView').src = '/api/qr?size=300&url=' + encodeURIComponent(location.origin + fileUrl(e));
      show('qrOverlay');
    }});
    if (S.isPC && !e.missing) items.push({ label: '📂 打开所在文件夹', fn: () =>
      api('/api/internal/reveal', { method: 'POST', body: JSON.stringify({ id: e.id }) }).catch(() => toast('打开失败')) });
  }
  items.push({ label: '🗑 移除', fn: async () => {
    try {
      await api('/api/stream/' + e.id, { method: 'DELETE' });
      S.entries = S.entries.filter(x => x.id !== e.id);
      renderStream();
    } catch { toast('移除失败'); }
  }});
  menuSheet(items);
}

/* 发送文本 */
async function sendText() {
  const ta = $('#textInput');
  const text = ta.value.trim();
  if (!text) return;
  try {
    await api('/api/stream/text', { method: 'POST', body: JSON.stringify({ text, origin: S.isPC ? 'pc' : 'phone' }) });
    ta.value = '';
    ta.style.height = 'auto';
    setTyping();
  } catch { toast('发送失败'); }
}
$('#sendBtn').onclick = sendText;
$('#textInput').addEventListener('keydown', ev => {
  if (ev.key === 'Enter' && !ev.shiftKey) { ev.preventDefault(); sendText(); }
});
$('#textInput').addEventListener('input', ev => {
  ev.target.style.height = 'auto';
  ev.target.style.height = Math.min(120, ev.target.scrollHeight) + 'px';
  setTyping();
});
// 输入文字时隐藏悬浮 ＋ 按钮，避免挡住「发送」（仅传输页生效）
function setTyping() {
  const typing = S.tab === 'stream' && (document.activeElement === $('#textInput') || $('#textInput').value.length > 0);
  document.body.classList.toggle('typing', typing);
}
$('#textInput').addEventListener('focus', setTyping);
$('#textInput').addEventListener('blur', () => setTimeout(setTyping, 150));

/* ================= 播放器（倍速/连播/字幕/转码） ================= */

function openPlayer(name, url, opts = {}) {
  $('#playerName').textContent = name;
  $('#tcBtn').classList.add('hidden');
  const v = $('#player');
  v.dataset.triedTc = '';
  v.onerror = () => {
    // mkv/avi 等浏览器放不了 → 提供转码播放（需电脑装有 ffmpeg）
    if (['mkv', 'avi'].includes(ext(name)) && !v.dataset.triedTc && opts.tc) {
      v.dataset.triedTc = '1';
      $('#tcBtn').classList.remove('hidden');
      toast('此格式浏览器不支持，可点「转码播放」');
    } else {
      toast('播放失败，可下载后用本地播放器打开');
    }
  };
  v.onloadedmetadata = () => {
    const posKey = 'pos:' + url;
    const saved = parseFloat(localStorage.getItem(posKey));
    if (saved > 10 && saved < v.duration - 10) v.currentTime = saved;
  };
  v.ontimeupdate = () => { if (v.currentTime > 5) localStorage.setItem('pos:' + url, v.currentTime); };
  v.onended = () => { // 同目录连播
    if (!opts.queue || opts.queue.length < 2) return;
    const idx = opts.queue.findIndex(x => x.url === url);
    const next = opts.queue[idx + 1];
    if (next) {
      $('#nextHint').textContent = '即将播放：' + next.name;
      setTimeout(() => openPlayer(next.name, next.url, { ...opts, queue: opts.queue, tc: next.tc, subId: next.subId, subPath: next.subPath }), 800);
    }
  };
  // 队列与连播提示
  if (opts.queue && opts.queue.length > 1) {
    const idx = opts.queue.findIndex(x => x.url === url);
    const next = opts.queue ? opts.queue[idx + 1] : null;
    $('#nextHint').textContent = next ? '下一个：' + next.name : '';
  } else {
    $('#nextHint').textContent = '';
  }
  v.src = url;
  setSpeed(1);
  // 字幕：找同主名 .srt/.vtt（条目或浏览路径）
  attachSubtitle(v, opts);
  // 转码按钮
  $('#tcBtn').onclick = () => {
    v.dataset.triedTc = '1';
    $('#tcBtn').classList.add('hidden');
    toast('正在通过电脑 ffmpeg 转封装播放…');
    v.onerror = () => toast('转码播放失败（源编码浏览器不支持或电脑未装 ffmpeg）');
    v.src = opts.tc;
    v.play().catch(() => {});
  };
  show('playerOverlay');
  v.play().catch(() => {});
}

async function attachSubtitle(v, opts) {
  try {
    let url = null;
    if (opts.subId) url = `/api/sub?id=${opts.subId}`;
    else if (opts.subPath) url = `/api/sub?vpath=${encodeURIComponent(opts.subPath)}`;
    if (!url) return;
    const r = await fetch(url);
    if (!r.ok) return;
    const blob = new Blob([await r.text()], { type: 'text/vtt' });
    const track = document.createElement('track');
    track.kind = 'subtitles'; track.label = '字幕'; track.default = true;
    track.src = URL.createObjectURL(blob);
    if (v.textContent !== undefined && v.querySelector('track')) v.querySelector('track').remove();
    v.appendChild(track);
    if (v.textTracks[0]) v.textTracks[0].mode = 'showing';
  } catch {}
}

function setSpeed(rate) {
  const v = $('#player');
  v.playbackRate = rate;
  $$('#speedRow button[data-rate]').forEach(b => b.classList.toggle('on', +b.dataset.rate === rate));
}
$('#speedRow').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-rate]');
  if (b) setSpeed(+b.dataset.rate);
});

/* ================= 图片预览 / 文本查看 ================= */

function openImageList(list, index) {
  S.images = list;
  S.imgIndex = Math.max(0, index);
  if (!S.images.length) return;
  $('#imgView').src = S.images[S.imgIndex].url;
  show('imageOverlay');
}
$('#imgPrev').onclick = () => { if (S.imgIndex > 0) { S.imgIndex--; $('#imgView').src = S.images[S.imgIndex].url; } };
$('#imgNext').onclick = () => { if (S.imgIndex < S.images.length - 1) { S.imgIndex++; $('#imgView').src = S.images[S.imgIndex].url; } };
let touchX = null;
$('#imageOverlay').addEventListener('touchstart', ev => touchX = ev.touches[0].clientX, { passive: true });
$('#imageOverlay').addEventListener('touchend', ev => {
  if (touchX == null) return;
  const dx = ev.changedTouches[0].clientX - touchX;
  if (dx < -40) $('#imgNext').click();
  if (dx > 40) $('#imgPrev').click();
  touchX = null;
}, { passive: true });

async function openText(name, url, size) {
  if (size > 2 * 1048576) { toast('文件太大，直接下载查看'); downloadUrl(url + '?dl=1', name); return; }
  $('#textName').textContent = name;
  $('#textView').textContent = '加载中…';
  show('textOverlay');
  try {
    const r = await fetch(url);
    $('#textView').textContent = r.ok ? await r.text() : '读取失败';
  } catch { $('#textView').textContent = '读取失败'; }
}
$('#textCopyBtn').onclick = async () => {
  const ok = await copyText($('#textView').textContent);
  toast(ok ? '已复制' : '复制失败');
};

/* ================= 通用菜单 ================= */

function menuSheet(items) {
  const sheet = $('#menuSheet');
  sheet.innerHTML = items.map((it, i) => `<button class="mitem" data-i="${i}">${it.label}</button>`).join('');
  show('menuOverlay');
  sheet.onclick = ev => {
    const b = ev.target.closest('.mitem');
    if (!b) return;
    hide('menuOverlay');
    const it = items[+b.dataset.i];
    it.fn && it.fn();
  };
}

/* ================= 文件浏览 ================= */

async function loadDir(path) {
  S.searchMode = false;
  $('#searchInput').value = '';
  exitSelectMode();
  try {
    const d = await api('/api/fs/list?path=' + encodeURIComponent(path));
    S.cwd = d.path || '/';
    S.rows = (d.entries || []).map(it => it.dir
      ? { dir: true, name: it.name, path: it.path || joinPath(S.cwd, it.name), sub: it.sub }
      : { dir: false, name: it.name, path: it.path || joinPath(S.cwd, it.name), size: it.size });
    renderCrumbs();
    renderRows();
    renderFavorites();
    pushRecent(S.cwd);
  } catch { toast('读取目录失败'); }
}

function rootLabelOf(root) {
  const r = String(root || '').replace(/[\\/]+$/, '');
  if (/^[A-Za-z]:$/.test(r)) return r[0].toUpperCase() + ' 盘';
  const b = r.split(/[\\/]/).filter(Boolean).pop();
  return b || root;
}
function rootLabelByIdx(idx) {
  const roots = (S.settings && S.settings.shareRoots) || [];
  return roots[+idx] ? rootLabelOf(roots[+idx]) : '';
}

function renderCrumbs() {
  if (S.cwd === '/') {
    $('#crumbs').innerHTML = '<span class="hint">所有共享位置</span>';
  } else {
    const segs = S.cwd.split('/').filter(Boolean);
    let html = `<button class="crumb" data-p="/">所有位置</button>`;
    let acc = '';
    segs.forEach((seg, i) => {
      acc += '/' + seg;
      const label = i === 0 ? (rootLabelByIdx(seg) || seg) : seg;
      html += ` <span>›</span> <button class="crumb" data-p="${esc(acc)}">${esc(label)}</button>`;
    });
    $('#crumbs').innerHTML = html;
  }
  const favored = S.favorites.some(f => f.path === S.cwd);
  $('#pinBtn2').textContent = favored ? '★ 已收藏' : '☆ 收藏';
}
$('#crumbs').addEventListener('click', ev => {
  const b = ev.target.closest('.crumb');
  if (b) loadDir(b.dataset.p);
});

function renderRows() {
  const sel = S.sel.on;
  $('#fileList').innerHTML = S.rows.map((r, i) => {
    const checked = S.sel.paths.has(r.path);
    const k = r.dir ? 'dir' : kindOf(r.name);
    const visual = !r.dir && k === 'image'
      ? `<img class="thumb small" loading="lazy" src="/api/thumb?vpath=${encodeURIComponent(r.path)}&w=120" alt="">`
      : `<span class="cicon">${ICONS[k] || '📦'}</span>`;
    const dur = !r.dir && k === 'video' ? `<i class="durbadge" data-dur-path="${esc(r.path)}"></i>` : '';
    return `<div class="fitem ${r.dir ? 'isdir' : ''} ${checked ? 'selon' : ''}" data-i="${i}">
      ${sel ? `<span class="chkbox ${checked ? 'on' : ''}">${checked ? '✓' : ''}</span>` : ''}
      ${visual}
      <span class="fname">${esc(r.name)}${r.sub ? `<span class="fsub">${esc(r.sub)}</span>` : ''} ${dur}</span>
      <span class="fmeta">${r.dir ? '' : fmtSize(r.size)}</span>
    </div>`;
  }).join('') || '<div class="empty"><p class="hint">空目录</p></div>';
  $('#selCount').textContent = `已选 ${S.sel.paths.size} 项`;
  // 视频时长懒取（前 20 个）
  if (S.settings && S.settings.ffmpeg) {
    $$('#fileList [data-dur-path]').forEach((el, i) => {
      if (i >= 20) return;
      api(`/api/meta?vpath=${encodeURIComponent(el.dataset.durPath)}`).then(d => { if (d.duration) el.textContent = fmtDur(d.duration); }).catch(() => {});
    });
  }
}

$('#fileList').addEventListener('click', ev => {
  const el = ev.target.closest('.fitem');
  if (!el) return;
  const r = S.rows[+el.dataset.i];
  if (!r) return;
  if (S.sel.on) { toggleSel(r); return; }
  if (r.dir) loadDir(r.path);
  else openFsFile(r);
});

function openFsFile(r) {
  const url = fsFileUrl(r.path);
  const k = kindOf(r.name);
  if (k === 'video' || k === 'audio') {
    const vids = S.rows.filter(x => !x.dir && ['video', 'audio'].includes(kindOf(x.name)))
      .map(x => ({ name: x.name, url: fsFileUrl(x.path), tc: tcFsUrl(x.path), subPath: x.path }));
    openPlayer(r.name, url, { queue: vids, tc: tcFsUrl(r.path), subPath: r.path });
  } else if (k === 'image') {
    const imgs = S.rows.filter(x => !x.dir && kindOf(x.name) === 'image');
    openImageList(imgs.map(x => ({ name: x.name, url: fsFileUrl(x.path) })), Math.max(0, imgs.findIndex(x => x.path === r.path)));
  } else if (k === 'text') {
    openText(r.name, url, r.size);
  } else {
    downloadUrl(url + '?dl=1', r.name);
  }
}

function fsMenu(r) {
  const items = [];
  if (r.dir) {
    items.push({ label: '📂 打开', fn: () => loadDir(r.path) });
    items.push({ label: '📦 打包下载 (zip)', fn: () => zipDownload([r.path]) });
  } else {
    items.push({ label: '⬇ 下载', fn: () => downloadUrl(fsFileUrl(r.path) + '?dl=1', r.name) });
    items.push({ label: '🔗 复制链接', fn: async () => {
      const ok = await copyText(location.origin + fsFileUrl(r.path));
      toast(ok ? '已复制链接' : '复制失败');
    }});
    items.push({ label: '📱 生成二维码', fn: () => {
      $('#qrView').src = '/api/qr?size=300&url=' + encodeURIComponent(location.origin + fsFileUrl(r.path));
      show('qrOverlay');
    }});
    items.push({ label: '➕ 发到传输流', fn: async () => {
      try { await api('/api/stream/ref', { method: 'POST', body: JSON.stringify({ path: r.path }) }); toast('已上架到传输流'); }
      catch { toast('上架失败'); }
    }});
  }
  menuSheet(items);
}

/* 长按 / 右键菜单 */
let lpTimer;
document.addEventListener('pointerdown', ev => {
  const el = ev.target.closest('.fitem, .mitem2, #streamList .card, .mediaitem');
  if (!el) return;
  lpTimer = setTimeout(() => showMenuForEl(el), 550);
});
['pointerup', 'pointercancel', 'pointermove'].forEach(t =>
  document.addEventListener(t, () => clearTimeout(lpTimer)));
document.addEventListener('contextmenu', ev => {
  const el = ev.target.closest('.fitem, .mitem2, #streamList .card, .mediaitem');
  if (!el) return;
  ev.preventDefault();
  showMenuForEl(el);
});
function showMenuForEl(el) {
  if (el.classList.contains('fitem')) fsMenu(S.rows[+el.dataset.i] || {});
  else if (el.classList.contains('mediaitem')) mediaMenu(S.mediaFlat[+el.dataset.i] || {});
  else {
    const e = S.entries.find(x => x.id === el.dataset.id);
    if (e) streamMenu(e);
  }
}

/* ---------- 多选 ---------- */
$('#selModeBtn').onclick = () => { S.sel.on ? exitSelectMode() : enterSelectMode(); };
function enterSelectMode() {
  S.sel.on = true;
  S.sel.paths = new Set();
  $('#selectBar').classList.remove('hidden');
  $('#selModeBtn').textContent = '✓ 完成';
  renderRows();
}
function exitSelectMode() {
  S.sel.on = false;
  S.sel.paths = new Set();
  $('#selectBar').classList.add('hidden');
  $('#selModeBtn').textContent = '☰ 多选';
  renderRows();
}
function toggleSel(r) {
  if (S.sel.paths.has(r.path)) S.sel.paths.delete(r.path);
  else S.sel.paths.add(r.path);
  renderRows();
}
$('#selCancelBtn').onclick = exitSelectMode;
$('#selZipBtn').onclick = () => {
  const paths = [...S.sel.paths];
  if (!paths.length) return toast('先选择条目');
  zipDownload(paths);
};
$('#selStreamBtn').onclick = async () => {
  const paths = [...S.sel.paths].filter(p => !S.rows.find(r => r.path === p && r.dir));
  if (!paths.length) return toast('只能发文件（文件夹请用打包下载）');
  let n = 0;
  for (const p of paths) {
    try { await api('/api/stream/ref', { method: 'POST', body: JSON.stringify({ path: p }) }); n++; } catch {}
  }
  toast(`已上架 ${n} 个`);
  exitSelectMode();
};

async function zipDownload(paths) {
  try {
    const d = await api('/api/zip', { method: 'POST', body: JSON.stringify({ paths }) });
    downloadUrl(d.url, 'trans.zip');
  } catch (e) { toast(e.message || '打包失败'); }
}

/* ---------- 搜索 ---------- */
let searchTimer;
$('#searchInput').addEventListener('input', () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(async () => {
    const q = $('#searchInput').value.trim();
    if (!q) { loadDir(S.cwd); return; }
    try {
      const d = await api('/api/fs/search?q=' + encodeURIComponent(q));
      S.searchMode = true;
      S.rows = (d.results || []).map(r => ({ dir: false, name: r.name, path: r.path, size: r.size }));
      $('#crumbs').innerHTML = `<span class="hint">搜索「${esc(q)}」 · ${S.rows.length} 个结果${d.truncated ? '（结果过多已截断）' : ''}</span>`;
      exitSelectMode();
      renderRows();
    } catch {}
  }, 300);
});

/* ---------- 收藏 ---------- */
$('#pinBtn2').onclick = async () => {
  if (S.cwd === '/') { toast('先进入要收藏的目录'); return; }
  const exists = S.favorites.some(f => f.path === S.cwd);
  try {
    const d = await api('/api/favorites', { method: 'POST', body: JSON.stringify({ path: S.cwd, remove: exists }) });
    S.favorites = d.favorites || [];
    renderFavorites();
    renderCrumbs();
    toast(exists ? '已取消收藏' : '已收藏');
  } catch { toast('操作失败'); }
};
function renderFavorites() {
  const row = $('#pinnedRow');
  if (!S.favorites || !S.favorites.length) { row.classList.add('hidden'); return; }
  row.classList.remove('hidden');
  row.innerHTML = S.favorites.map(f =>
    `<button class="chip" data-p="${esc(f.path)}" data-dir="${f.dir ? 1 : 0}">⭐ ${esc(f.name)}</button>`).join('');
}
$('#pinnedRow').addEventListener('click', ev => {
  const b = ev.target.closest('.chip');
  if (!b) return;
  switchTab('files');
  if (b.dataset.dir === '1') loadDir(b.dataset.p);
  else openFsFile({ name: b.textContent.replace(/^⭐\s*/, ''), path: b.dataset.p, size: 0 });
});

/* ---------- 最近打开 ---------- */
function pushRecent(path) {
  if (path === '/') return;
  const name = path.split('/').filter(Boolean).pop();
  S.recent = [{ path, name }, ...S.recent.filter(x => x.path !== path)].slice(0, 6);
  localStorage.setItem('trans.recent', JSON.stringify(S.recent));
  renderRecent();
}
function renderRecent() {
  const row = $('#recentRow');
  if (!S.recent.length) { row.classList.add('hidden'); return; }
  row.classList.remove('hidden');
  row.innerHTML = '<span class="hint" style="align-self:center">最近</span>' +
    S.recent.map(r => `<button class="chip" data-p="${esc(r.path)}">🕘 ${esc(r.name)}</button>`).join('');
}
$('#recentRow').addEventListener('click', ev => {
  const b = ev.target.closest('.chip');
  if (b) loadDir(b.dataset.p);
});

/* ================= 相册（媒体瀑布流） ================= */

let mediaShown = 30;
async function loadMedia() {
  $('#mediaSummary').textContent = '扫描中…';
  try {
    const d = await api('/api/media');
    S.media = d.groups || [];
    mediaShown = 30;
    renderMedia();
    $('#mediaSummary').textContent = `${S.media.length} 个目录${d.truncated ? '（部分扫描）' : ''}`;
  } catch { $('#mediaSummary').textContent = '扫描失败'; }
}
function renderMedia() {
  S.mediaFlat = [];
  const shown = S.media.slice(0, mediaShown);
  $('#mediaList').innerHTML = shown.map(g => {
    const seg1 = g.dir.split('/')[1] || '';
    const label = /^\/\d+$/.test('/' + seg1)
      ? rootLabelByIdx(seg1) + g.dir.slice(('/' + seg1).length)
      : g.dir;
    return `<div class="mgroup">
      <div class="mhead"><button class="crumb" data-p="${esc(g.dir)}">${esc(label)}</button><span class="hint">${g.count} 项</span></div>
      <div class="mgrid">${g.items.map(it => {
        S.mediaFlat.push({ ...it, group: g });
        const idx = S.mediaFlat.length - 1;
        const isImg = it.kind === 'image';
        return `<div class="mediaitem ${isImg ? '' : 'vid'}" data-i="${idx}">
          ${isImg
            ? `<img loading="lazy" src="/api/thumb?vpath=${encodeURIComponent(it.path)}&w=256" alt="${esc(it.name)}">`
            : `<div class="mvcover">🎬<span>${esc(it.name)}</span></div>`}
        </div>`;
      }).join('')}</div>
    </div>`;
  }).join('') +
  (mediaShown < S.media.length
    ? `<div class="empty"><button class="mini" id="mediaMore">加载更多（还有 ${S.media.length - mediaShown} 组）</button></div>`
    : '') || '<div class="empty"><p class="hint">共享位置里没找到图片/视频</p></div>';
  const more = $('#mediaMore');
  if (more) more.onclick = () => { mediaShown += 30; renderMedia(); };
}
$('#mediaRefresh').onclick = () => loadMedia();
$('#mediaList').addEventListener('click', ev => {
  const head = ev.target.closest('.mhead .crumb');
  if (head) { switchTab('files'); loadDir(head.dataset.p); return; }
  const el = ev.target.closest('.mediaitem');
  if (!el) return;
  const it = S.mediaFlat[+el.dataset.i];
  if (!it) return;
  if (it.kind === 'image') {
    const imgs = it.group.items.filter(x => x.kind === 'image');
    openImageList(imgs.map(x => ({ name: x.name, url: fsFileUrl(x.path) })), Math.max(0, imgs.findIndex(x => x.path === it.path)));
  } else {
    const vids = it.group.items.filter(x => x.kind === 'video')
      .map(x => ({ name: x.name, url: fsFileUrl(x.path), tc: tcFsUrl(x.path), subPath: x.path }));
    openPlayer(it.name, fsFileUrl(it.path), { queue: vids, tc: tcFsUrl(it.path), subPath: it.path });
  }
});
function mediaMenu(it) {
  menuSheet([
    { label: '⬇ 下载', fn: () => downloadUrl(fsFileUrl(it.path) + '?dl=1', it.name) },
    { label: '➕ 发到传输流', fn: async () => {
      try { await api('/api/stream/ref', { method: 'POST', body: JSON.stringify({ path: it.path }) }); toast('已上架'); } catch { toast('上架失败'); }
    }},
    { label: '📂 打开所在目录', fn: () => { switchTab('files'); loadDir(it.dir0 || it.group.dir); } },
  ]);
}

/* ================= 上传（小文件直传 / 大文件分片续传） ================= */

$('#fab').onclick = () => {
  const onFilesTab = S.tab === 'files' && !S.searchMode;
  $('#destDirOpt').style.display = onFilesTab ? '' : 'none';
  if (!onFilesTab) $('#destRow').querySelector('input[value=stream]').checked = true;
  renderQueue();
  show('uploadOverlay');
};
$('#pickMedia').onclick = () => $('#fileInput').click();
$('#pickCam').onclick = () => $('#camInput').click();
$('#fileInput').onchange = ev => { addUploads([...ev.target.files]); ev.target.value = ''; };
$('#camInput').onchange = ev => { addUploads([...ev.target.files]); ev.target.value = ''; };

function addUploads(files) {
  const dest = $('#destRow').querySelector('input[name=dest]:checked')?.value || 'stream';
  for (const f of files) {
    S.uploads.push({
      id: Math.random().toString(36).slice(2),
      file: f, name: f.name, size: f.size,
      progress: 0, status: 'waiting', err: '', xhr: null, resolve: '', dest,
    });
  }
  renderQueue();
  pumpUploads();
}

function pumpUploads() {
  $('#uploadHint').classList.toggle('hidden', !S.uploads.some(i => i.status === 'uploading' || i.status === 'waiting'));
  const running = S.uploads.filter(i => i.status === 'uploading').length;
  const waiting = S.uploads.filter(i => i.status === 'waiting');
  for (let i = 0; i < Math.min(2 - running, waiting.length); i++) uploadOne(waiting[i]);
}

const CHUNK_THRESHOLD = 20 * 1048576;
const CHUNK_SIZE = 4 * 1048576;

async function uploadOne(item) {
  try {
    const sp = await api('/api/space');
    if (sp.free < item.size + 50 * 1048576) {
      item.status = 'error';
      item.err = `电脑磁盘空间不足（剩 ${fmtSize(sp.free)}）`;
      renderQueue();
      return;
    }
  } catch {}
  if (item.size > CHUNK_THRESHOLD) return uploadChunked(item);
  return uploadSimple(item);
}

function uploadSimple(item) {
  const fd = new FormData();
  fd.append('dest', item.dest);
  if (item.dest === 'dir') fd.append('dir', S.cwd);
  if (item.resolve) fd.append('resolve', item.resolve);
  fd.append('origin', S.isPC ? 'pc-drag' : 'phone');
  fd.append('file', item.file, item.name);
  const xhr = new XMLHttpRequest();
  item.xhr = xhr;
  item.status = 'uploading';
  item.progress = 0;
  renderQueue();
  xhr.upload.onprogress = ev => { if (ev.lengthComputable) { item.progress = ev.loaded / ev.total; renderQueue(); } };
  xhr.onload = () => finishUploadByStatus(item, xhr.status);
  xhr.onerror = () => { item.status = 'error'; item.err = '网络中断'; renderQueue(); pumpUploads(); };
  xhr.onabort = () => { item.status = 'canceled'; item.err = '已取消'; renderQueue(); pumpUploads(); };
  xhr.open('POST', '/api/upload');
  xhr.send(fd);
}

async function uploadChunked(item) {
  item.status = 'uploading';
  item.progress = 0;
  renderQueue();
  try {
    const init = await api('/api/upload/init', {
      method: 'POST',
      body: JSON.stringify({ name: item.name, size: item.size, dest: item.dest, dir: item.dest === 'dir' ? S.cwd : '' }),
    });
    let offset = init.received || 0;
    while (offset < item.size) {
      if (item.status === 'canceled') return;
      const end = Math.min(offset + CHUNK_SIZE, item.size);
      const blob = item.file.slice(offset, end);
      offset = await sendChunk(item, init.id, offset, blob);
      item.progress = offset / item.size;
      renderQueue();
    }
    const done = await fetch(`/api/upload/complete?id=${init.id}${item.resolve ? '&resolve=' + item.resolve : ''}&origin=${S.isPC ? 'pc-drag' : 'phone'}`, { method: 'POST' });
    finishUploadByStatus(item, done.status);
  } catch (e) {
    if (item.status === 'canceled') return;
    item.status = 'error';
    item.err = e.message || '上传失败（可重试续传）';
    renderQueue();
    pumpUploads();
  }
}

// 发一个分片，带最多 5 次重试与进度对齐（断点续传）
function sendChunk(item, id, offset, blob) {
  return new Promise((resolve, reject) => {
    const attempt = remain => {
      const xhr = new XMLHttpRequest();
      item.xhr = xhr;
      xhr.upload.onprogress = ev => {
        if (ev.lengthComputable) { item.progress = (offset + ev.loaded) / item.size; renderQueue(); }
      };
      xhr.onload = async () => {
        if (xhr.status === 200) return resolve(offset + blob.size);
        if (xhr.status === 409) { // 服务端进度和本地不一致 → 对齐后续传
          let received = offset;
          try { received = (await api(`/api/upload/status?id=${id}`)).received; } catch {}
          offset = received;
          return resolve(received);
        }
        if (remain > 0) return setTimeout(() => attempt(remain - 1), 1000);
        reject(new Error('分片上传失败'));
      };
      xhr.onerror = () => { if (remain > 0) return setTimeout(() => attempt(remain - 1), 1500); reject(new Error('网络中断')); };
      xhr.onabort = () => { item.status = 'canceled'; item.err = '已取消（重试可续传）'; reject(new Error('canceled')); };
      xhr.open('PUT', `/api/upload/chunk?id=${id}&offset=${offset}`);
      xhr.send(blob);
    };
    attempt(5);
  });
}

function finishUploadByStatus(item, status) {
  if (status === 200) { item.status = 'done'; }
  else if (status === 409) { item.status = 'conflict'; item.err = '同名冲突'; }
  else if (status === 507) { item.status = 'error'; item.err = '电脑磁盘空间不足'; }
  else if (status === 503) { item.status = 'error'; item.err = '服务已暂停'; }
  else { item.status = 'error'; item.err = '上传失败 (' + status + ')'; }
  renderQueue();
  pumpUploads();
}

function renderQueue() {
  const q = $('#queue');
  if (!S.uploads.length) { q.innerHTML = '<p class="hint" style="text-align:center;padding:8px 0">没有上传任务</p>'; return; }
  q.innerHTML = S.uploads.map(item => {
    const pct = Math.round((item.progress || 0) * 100);
    let right = '';
    if (item.status === 'uploading') {
      right = `<span>${pct}%</span><button class="mini" data-a="cancel" data-id="${item.id}">取消</button>`;
    } else if (item.status === 'waiting') {
      right = '<span class="hint">等待中</span>';
    } else if (item.status === 'conflict') {
      right = `<span style="color:var(--danger)">同名冲突</span>
        <button class="mini" data-a="ov" data-id="${item.id}">覆盖</button>
        <button class="mini" data-a="rn" data-id="${item.id}">改名</button>
        <button class="mini" data-a="sk" data-id="${item.id}">跳过</button>`;
    } else if (item.status === 'error' || item.status === 'canceled') {
      right = `<span class="err">${esc(item.err)}</span>
        <button class="mini" data-a="retry" data-id="${item.id}">重试</button>
        <button class="mini" data-a="drop" data-id="${item.id}">✕</button>`;
    } else if (item.status === 'done') {
      right = `<span class="ok2">✓ 完成</span><button class="mini" data-a="drop" data-id="${item.id}">✕</button>`;
    }
    return `<div class="qitem">
      <div class="qinfo">
        <div><div class="cname">${esc(item.name)}</div>
        <span class="hint">${fmtSize(item.size)} → ${item.dest === 'stream' ? '传输流' : '当前目录'}${item.size > CHUNK_THRESHOLD ? ' · 分片' : ''}</span></div>
        <div class="qbtns">${right}</div>
      </div>
      ${item.status === 'uploading' ? `<div class="bar"><i style="width:${pct}%"></i></div>` : ''}
    </div>`;
  }).join('');
  $('#uploadHint').classList.toggle('hidden', !S.uploads.some(i => i.status === 'uploading' || i.status === 'waiting'));
}

$('#queue').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-a]');
  if (!b) return;
  const item = S.uploads.find(i => i.id === b.dataset.id);
  if (!item) return;
  switch (b.dataset.a) {
    case 'cancel': item.xhr && item.xhr.abort(); break;
    case 'retry': item.status = 'waiting'; item.err = ''; item.progress = 0; renderQueue(); pumpUploads(); break;
    case 'drop': S.uploads = S.uploads.filter(i => i.id !== item.id); renderQueue(); pumpUploads(); break;
    case 'ov': item.resolve = 'overwrite'; item.status = 'waiting'; renderQueue(); pumpUploads(); break;
    case 'rn': item.resolve = 'rename'; item.status = 'waiting'; renderQueue(); pumpUploads(); break;
    case 'sk': item.status = 'done'; item.err = ''; renderQueue(); break;
  }
});

/* ================= PC 拖拽上架（含文件夹递归） ================= */

let dragDepth = 0;
window.addEventListener('dragenter', ev => { ev.preventDefault(); dragDepth++; $('#dropOverlay').classList.remove('hidden'); });
window.addEventListener('dragleave', ev => {
  ev.preventDefault();
  if (--dragDepth <= 0) { dragDepth = 0; $('#dropOverlay').classList.add('hidden'); }
});
window.addEventListener('dragover', ev => ev.preventDefault());
window.addEventListener('drop', async ev => {
  ev.preventDefault();
  dragDepth = 0;
  $('#dropOverlay').classList.add('hidden');
  const files = await collectDroppedFiles(ev.dataTransfer);
  if (!files.length) return;
  $('#destRow').querySelector('input[value=stream]').checked = true;
  $('#destDirOpt').style.display = 'none';
  addUploads(files.slice(0, 300));
  show('uploadOverlay');
});

// webkitGetAsEntry 递归展开文件夹（Chrome/Edge）；不支持时退回普通文件列表
async function collectDroppedFiles(dt) {
  const out = [];
  try {
    const entries = [...(dt.items || [])].map(i => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
    if (!entries.length) return [...dt.files];
    const walk = async (entry, prefix) => {
      if (out.length >= 300) return;
      if (entry.isFile) {
        const f = await new Promise((res, rej) => entry.file(res, rej)).catch(() => null);
        if (f) out.push(prefix ? new File([f], prefix + '/' + f.name, { type: f.type }) : f);
      } else if (entry.isDirectory) {
        const rd = entry.createReader();
        let batch;
        do {
          batch = await new Promise((res, rej) => rd.readEntries(res, rej)).catch(() => []);
          for (const e of batch) await walk(e, prefix ? prefix + '/' + entry.name : entry.name);
        } while (batch.length && out.length < 300);
      }
    };
    for (const e of entries) await walk(e, '');
    if (out.length) return out;
  } catch {}
  return [...dt.files];
}

/* ================= PC 控制台侧栏 ================= */

function renderConsole() {
  if (!S.net || !S.settings) return;
  const ifs = S.net.interfaces || [];
  $('#ipSelect').innerHTML = ifs.length
    ? ifs.map((n, i) => `<option value="${i}">${esc(n.name)} · ${n.ip}</option>`).join('')
    : '<option>未找到局域网 IP</option>';
  $('#verTag').textContent = 'v' + (S.settings.version || '');
  $('#autostartChk').checked = !!S.settings.autostart;
  $('#sendtoState').textContent = S.settings.sendtoInstalled
    ? '✓ 已安装右键「发送到 Trans」'
    : '⚠ 未安装右键菜单（命令行运行 trans.exe --install-sendto）';
  $('#clipWatchChk').checked = !!S.settings.clipWatch;
  updateClipPauseBtn(S.settings.clipPausedUntil || 0);
  $('#pinState').textContent = S.settings.pinSet ? '🔒 已启用（本局域网访问需输 PIN）' : '未启用';
  $('#ffmpegState').textContent = S.settings.ffmpeg
    ? '✓ ffmpeg 可用（mkv 转码播放、视频时长）'
    : '⚠ 未安装 ffmpeg：mkv 只能下载后播、视频不显示时长（安装后加入 PATH 重启即用）';
  renderRoots();
  updateQr();
  updateCache();
}

function updateClipPauseBtn(until) {
  const paused = until > Date.now();
  $('#clipPauseBtn').textContent = paused ? `已暂停 ${Math.ceil((until - Date.now()) / 60000)} 分钟` : '暂停10分钟';
}
$('#clipPauseBtn').onclick = async () => {
  // 暂停/恢复都走服务端（暂停中复制不会上墙）
  try {
    const st = await api('/api/settings');
    if (st.clipPausedUntil > Date.now()) {
      // 用一个 0 长度暂停表示恢复
      await api('/api/clip/pause', { method: 'POST', body: JSON.stringify({ ms: 0 }) });
      updateClipPauseBtn(0);
      toast('已恢复监听');
    } else {
      await api('/api/clip/pause', { method: 'POST', body: JSON.stringify({ ms: 600000 }) });
      updateClipPauseBtn(Date.now() + 600000);
      toast('监听已暂停 10 分钟');
    }
  } catch { toast('操作失败'); }
};
$('#clipWatchChk').onchange = async ev => {
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ clipWatch: ev.target.checked }) });
    toast(ev.target.checked ? '已开启剪贴板监听' : '已关闭剪贴板监听');
  } catch { ev.target.checked = !ev.target.checked; toast('设置失败'); }
};
$('#pinBtn').onclick = async () => {
  const pin = prompt('设置访问 PIN（留空=取消 PIN）。启用后本局域网所有访问都要输 PIN');
  if (pin === null) return;
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ pin: pin.trim() }) });
    await refreshSettings();
    toast(pin.trim() ? 'PIN 已启用' : 'PIN 已取消');
  } catch (e) { toast(e.message || '设置失败'); }
};

/* 共享位置管理 */
function renderRoots() {
  const roots = (S.settings && S.settings.shareRoots) || [];
  $('#rootList').innerHTML = roots.map((r, i) => `
    <div class="rootitem">
      <span class="mono">${esc(r)}</span>
      <button class="mini danger" data-root="${i}" title="移除">✕</button>
    </div>`).join('') || '<p class="hint">无</p>';
}
$('#rootList').addEventListener('click', async ev => {
  const b = ev.target.closest('button[data-root]');
  if (!b) return;
  const roots = (S.settings.shareRoots || []).filter((_, i) => i !== +b.dataset.root);
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ shareRoots: roots }) });
    await refreshSettings();
    loadDir('/');
    toast('已移除');
  } catch (e) { toast(e.message || '移除失败'); }
});
$('#addRootBtn').onclick = async () => {
  const p = prompt('输入要共享的目录（绝对路径），例如 D:\\ 或 D:\\Movies');
  if (!p) return;
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ shareRoots: [...(S.settings.shareRoots || []), p.trim()] }) });
    await refreshSettings();
    loadDir('/');
    toast('已添加共享位置');
  } catch (e) { toast(e.message || '添加失败'); }
};
async function refreshSettings() {
  S.settings = await api('/api/settings');
  S.favorites = S.settings.favorites || [];
  renderConsole();
  renderFavorites();
}

function updateQr() {
  const ifs = (S.net && S.net.interfaces) || [];
  const n = ifs[+$('#ipSelect').value] || ifs[0];
  if (!n) { $('#qrImg').removeAttribute('src'); $('#urlText').textContent = '未找到局域网 IP'; return; }
  $('#qrImg').src = '/api/qr?size=260&url=' + encodeURIComponent(n.url);
  $('#urlText').textContent = n.url;
}
$('#ipSelect').onchange = updateQr;
$('#copyUrl').onclick = async () => {
  const ok = await copyText($('#urlText').textContent);
  toast(ok ? '已复制' : '复制失败');
};
function updateCache() {
  if (!S.settings) return;
  const used = S.settings.cacheUsed || 0;
  const limit = (S.settings.cacheLimitGB || 20) * 1073741824;
  $('#cacheBar').style.width = Math.min(100, used / limit * 100) + '%';
  $('#cacheText').textContent = `${fmtSize(used)} / ${S.settings.cacheLimitGB} GB`;
}
$('#clearCache').onclick = async () => {
  if (!confirm('清理暂存副本？传输流里的「副本」条目会被删除，「引用」条目不受影响')) return;
  try {
    const d = await api('/api/cache/clear', { method: 'POST' });
    toast(`已清理 ${fmtSize(d.freed || 0)}`);
    S.settings = await api('/api/settings');
    updateCache();
  } catch { toast('清理失败'); }
};
$('#autostartChk').onchange = async ev => {
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ autostart: ev.target.checked }) });
    toast(ev.target.checked ? '将随登录自动启动' : '已关闭自启');
  } catch {
    ev.target.checked = !ev.target.checked;
    toast('设置失败');
  }
};

/* ================= 弹层通用 ================= */

document.addEventListener('click', ev => {
  const c = ev.target.closest('[data-close]');
  if (c) hide(c.dataset.close);
});
['playerOverlay', 'imageOverlay', 'textOverlay', 'menuOverlay', 'uploadOverlay'].forEach(id => {
  $('#' + id).addEventListener('click', ev => { if (ev.target.id === id) hide(id); });
});
document.addEventListener('keydown', ev => {
  if (ev.key === 'Escape') {
    ['menuOverlay', 'textOverlay', 'imageOverlay', 'playerOverlay', 'uploadOverlay', 'qrOverlay'].forEach(id => hide(id));
  }
  if (!$('#imageOverlay').classList.contains('hidden')) {
    if (ev.key === 'ArrowLeft') $('#imgPrev').click();
    if (ev.key === 'ArrowRight') $('#imgNext').click();
  }
});

bootstrap();
