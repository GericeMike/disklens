// 盘镜 DiskLens — 应用逻辑
// 双模式: Wails 绑定可用时走真实引擎；纯浏览器预览时回退演示数据（右上角演示标记）
import './taste/charts.js';
import { renderTreemap } from './treemap.js';
import { drives as demoDrives, dTree, cleanItems as demoClean, growth as demoGrowth, topFiles as demoTop } from './data.js';

const $ = (s) => document.querySelector(s);
const esc = (v) => String(v).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

const hasGo = () => typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App;
const api = () => window.go.main.App;

const state = {
  view: 'overview',
  treemapPath: null,   // 当前 treemap 展示的目录
  treemapRoot: null,   // 根（用于面包屑裁剪）
  demo: !hasGo(),
  overview: null,
};

// ---------- 格式化 ----------
function fmtGB(bytes) {
  const b = Math.abs(bytes);
  if (b >= 1024 ** 4) return (bytes / 1024 ** 4).toFixed(2) + ' TB';
  if (b >= 1024 ** 3) return (bytes / 1024 ** 3).toFixed(1) + ' GB';
  if (b >= 1024 ** 2) return (bytes / 1024 ** 2).toFixed(1) + ' MB';
  if (b >= 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return bytes + ' B';
}
function comma(v) { return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, ','); }
const RISK = { safe: ['安全', 'pos'], caution: ['谨慎', 'warn'], danger: ['危险', 'neg'], data: ['数据', ''], info: ['说明', ''] };
const riskPill = (r) => { const [t, cls] = RISK[r] || ['说明', '']; return `<span class="pill ${cls}">${t}</span>`; };

const RAMPS = {
  terminal: ['#2a425e', '#243a52', '#1f3246', '#1a2a3b', '#152231', '#111a26', '#0d141d', '#0a1017', '#080c11'],
  sheet: ['#cfe3e8', '#bdd8df', '#accdd6', '#9bc2cd', '#8ab7c4', '#79acb9', '#68a1af', '#5796a4', '#468b99'],
};
const curStyle = () => document.documentElement.dataset.style || 'terminal';

// ---------- 视图路由 ----------
function switchView(v) {
  state.view = v;
  document.querySelectorAll('.nav-item').forEach(a => a.setAttribute('aria-current', a.dataset.view === v ? 'page' : 'false'));
  $('#view-crumb').textContent = '盘镜 / ' + ({ overview: '概览', history: '历史', settings: '设置' }[v] || v);
  if (v === 'overview') renderOverview();
  if (v === 'history') renderHistory();
  if (v === 'settings') renderSettings();
}

// ---------- 概览 ----------
async function renderOverview() {
  const view = $('#view');
  let d = state.overview;
  if (!state.demo) {
    d = await api().GetOverview();
    state.overview = d;
  } else if (!d) {
    d = demoOverview();
    state.overview = d;
  }
  $('#freshness').textContent = d.ready ? `上次扫描 ${d.finishedAt}` : '尚未扫描';

  view.innerHTML = `
    <div class="title-row">
      <div>
        <h1>概览</h1>
        <div class="sub">${d.ready ? esc(d.finding) : '选择磁盘开始第一次扫描'}${state.demo ? '　<span class="demo-pill">演示数据</span>' : ''}</div>
      </div>
      <div style="display:flex;align-items:center;gap:16px;">
        <span class="stamp num">${d.ready ? esc(d.finishedAt) : ''}</span>
        <button class="btn primary" id="btn-rescan">重新扫描</button>
      </div>
    </div>
    <div class="kpis" id="drives" style="margin-bottom:var(--gap);"></div>
    <div class="grid-map">
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;" id="tm-title">方块地图</h3>
        <span class="meta">点击方块下钻 · 悬停查看大小</span></header>
        <div class="crumbbar" id="tm-crumb"></div>
        <div class="panel-body" id="treemap"></div>
      </section>
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;" id="dl-title">当前目录</h3>
        <span class="meta">点击行下钻 · ⓘ 看解释</span></header>
        <div class="panel-body" id="dirlist"></div>
      </section>
    </div>
    <section class="panel" style="margin:var(--gap) 0;">
      <header><h3 style="font-size:16px;font-weight:600;">可清理项</h3><span class="meta num" id="clean-total"></span></header>
      <div class="clean-grid" id="clean-rows"></div>
    </section>
    <div class="grid-low">
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;">两次扫描变化</h3><span class="meta" id="growth-legend"></span></header>
        <div id="growth-chart"></div>
      </section>
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;">最大文件</h3></header>
        <div class="table-wrap">
          <table>
            <thead><tr><th>文件</th><th class="n">大小</th><th class="n">修改时间</th></tr></thead>
            <tbody id="topfiles-body"></tbody>
          </table>
        </div>
      </section>
    </div>
    <section class="panel" style="margin-top:var(--gap);">
      <header><h3 style="font-size:16px;font-weight:600;">文件类型统计</h3><span class="meta">按占用排序</span></header>
      <div id="types-chart"></div>
    </section>`;

  $('#btn-rescan').addEventListener('click', openScanModal);
  watchTreemapSize();
  if (!d.ready) {
    $('#drives').innerHTML = `<div class="empty" style="grid-column:1/-1;">还没有扫描数据 —— 点击「重新扫描」开始</div>`;
    $('#treemap').innerHTML = '';
    $('#dirlist').innerHTML = '';
    $('#clean-rows').innerHTML = `<div class="empty-inline">扫描后这里会给出知识库判定的可清理清单</div>`;
    $('#growth-chart').innerHTML = `<div class="empty-inline">需要两次扫描才能对比</div>`;
    $('#topfiles-body').innerHTML = '';
    return;
  }

  // treemap: 默认展示本次扫描的实际根（子文件夹扫描时是子目录而非盘根）；
  // 盘概览水位条兼任 treemap 的盘切换标签
  const biggest = [...(d.drives || [])].sort((a, b) => (b.total - b.free) - (a.total - a.free))[0];
  const roots = (d.roots && d.roots.length) ? d.roots : (biggest ? [biggest.letter] : ['C:\\']);
  state.scanRoots = roots; // 面包屑顶界与盘切换定位用
  if (!state.treemapRoot || !roots.some(r => r.toLowerCase() === String(state.treemapRoot).toLowerCase())) {
    state.treemapRoot = roots[0];
  }
  renderDrives(d.drives, state.treemapRoot);
  renderTopFiles(d.topFiles);
  renderTypes(d.typeStats);
  renderClean();
  await loadTreemap(state.treemapRoot);
  renderGrowth();
}

// 地图尺寸跟随面板实际大小（窗口缩放/布局变化时重绘，消除“地图下方空白”）
let tmResizeObserver = null;
let tmResizeTimer = 0;
function watchTreemapSize() {
  const box = $('#treemap');
  if (!box) return;
  if (tmResizeObserver) tmResizeObserver.disconnect();
  tmResizeObserver = new ResizeObserver(() => {
    clearTimeout(tmResizeTimer);
    tmResizeTimer = setTimeout(() => {
      if (state.treemapPath) loadTreemap(state.treemapPath);
    }, 160);
  });
  tmResizeObserver.observe(box);
}

function renderDrives(drives, selected) {
  const wrap = $('#drives');
  // 盘概览兼任 treemap 的盘切换标签（§5: KPI 作为切换控件，选中项带标记线）
  wrap.setAttribute('role', 'tablist');
  wrap.innerHTML = (drives || []).map(d => {
    const used = d.total - d.free;
    const pct = Number.isFinite(d.usedPct) ? d.usedPct : 0;
    const ticks = [25, 50, 75].map(p => `<span class="water-tick" style="left:${p}%"></span>`).join('');
    // treemapRoot 可能是子文件夹根（D:\GJQMTTest），属该盘即视为正在查看
    const sel = String(selected || '').toLowerCase().startsWith(d.letter.toLowerCase());
    return `
      <div class="kpi" role="tab" aria-selected="${sel}" title="点击查看 ${esc(d.letter)} 的方块地图">
        <div class="kpi-label">${esc(d.letter)} 磁盘${sel ? ' · 正在查看' : ''}</div>
        <div class="kpi-value">${fmtGB(d.free)} <small>可用</small></div>
        <div class="kpi-pct">${pct.toFixed(1)}% 已用</div>
        <div class="water">
          <div class="water-track">${ticks}<div class="water-fill${d.atRisk ? ' at-risk' : ''}" style="width:${pct}%"></div></div>
          <div class="water-ends num"><span>已用 ${fmtGB(used)}</span><span class="free${d.atRisk ? ' at-risk' : ''}">剩 ${fmtGB(d.free)}</span></div>
        </div>
      </div>`;
  }).join('');
  wrap.querySelectorAll('.kpi').forEach((cell, i) => {
    cell.addEventListener('click', () => {
      const d = drives[i];
      const root = rootOfDrive(d.letter);
      if (!d || state.treemapRoot === root) return;
      state.treemapRoot = root;
      state.treemapOthers = null;
      wrap.querySelectorAll('.kpi').forEach((c, j) => c.setAttribute('aria-selected', j === i ? 'true' : 'false'));
      wrap.querySelectorAll('.kpi-label').forEach((l, j) => {
        l.textContent = `${drives[j].letter} 磁盘${j === i ? ' · 正在查看' : ''}`;
      });
      loadTreemap(root);
      renderClean(d.letter); // 可清理项跟随当前所选盘
    });
  });
}

function renderTopFiles(files) {
  $('#topfiles-body').innerHTML = (files || []).map(f => `
    <tr>
      <td><span class="truncate">${esc(f.path)}</span></td>
      <td class="n num">${fmtGB(f.size)}</td>
      <td class="n num" style="color:var(--muted)">${esc(f.modTime)}</td>
    </tr>`).join('');
}

function renderTypes(types) {
  const el = $('#types-chart');
  if (!types || !types.length) { el.innerHTML = `<div class="empty-inline">暂无数据</div>`; return; }
  Charts.barList(el, types.slice(0, 8).map(t => ({ label: `${t.category}（${comma(t.files)} 个）`, value: t.bytes })), {
    format: fmtGB, thin: false,
  });
}

async function renderClean(drive) {
  const el = $('#clean-rows');
  const cur = drive || state.treemapRoot || '';
  let items;
  if (!state.demo) {
    items = await api().GetCleanSummary(cur);
  } else {
    const pre = (cur || '').replace(/\\+$/, '').toUpperCase();
    items = demoClean.map((c, i) => ({
      entryId: 'demo' + i, name: c.name, risk: c.risk, why: c.reason,
      totalSize: c.size * 1024 ** 3, paths: [{ path: c.where, size: c.size * 1024 ** 3 }],
    })).filter(c => !pre || String(c.paths[0]?.path || '').toUpperCase().startsWith(pre));
  }
  const total = (items || []).filter(i => i.risk === 'safe').reduce((s, i) => s + i.totalSize, 0);
  $('#clean-total').textContent = items && items.length ? `🟢项合计 ${fmtGB(total)}` : '';
  el.innerHTML = (items || []).slice(0, 12).map(i => {
    const top = (i.paths || [])[0] || {};
    return `
    <div class="clean-row" data-path="${esc(top.path || '')}" style="cursor:pointer;">
      ${riskPill(i.risk)}
      <div class="cbody">
        <div class="cname">${esc(i.name)}</div>
        <div class="creason">${esc(i.why)}</div>
      </div>
      <div class="csize num">${fmtGB(i.totalSize)}</div>
    </div>`;
  }).join('') || `<div class="empty-inline">扫描后这里会给出建议</div>`;
  el.querySelectorAll('.clean-row').forEach(row => {
    row.addEventListener('click', () => openExplain(row.dataset.path));
  });
}

// ---------- treemap 下钻 ----------
// rootOfDrive 本次扫描中某盘的实际根：整盘扫描→盘根；子文件夹扫描→该盘的扫描根。
// 混合扫描（C:\ + D:\GJQMTTest）点 D 盘标签应落到 D:\GJQMTTest 而不是空的 D:\
function rootOfDrive(letter) {
  const ls = String(letter || '').toLowerCase();
  for (const r of state.scanRoots || []) {
    if (String(r).toLowerCase().startsWith(ls)) return r;
  }
  return letter;
}

// crumbSegs 面包屑段列表；顶层裁剪到所属扫描根——
// 存档只有 D:\GJQMTTest 时最高层就是它，不出现点了是空的盘根 D:
function crumbSegs(path, tops) {
  const clean = s => String(s).replace(/\\+$/, '');
  const p = clean(path);
  // 所属扫描根：最长前缀匹配（含自身）
  let top = '';
  (tops || []).forEach(r => {
    const rc = clean(r);
    const pn = p.toLowerCase(), rn = rc.toLowerCase();
    if ((pn === rn || pn.startsWith(rn + '\\')) && rc.length > top.length) top = rc;
  });
  if (!top) {
    // 无扫描根信息（demo 等）：退回盘符
    const m = p.match(/^[a-zA-Z]:/);
    top = m ? m[0] : p;
  }
  // 裸盘符顶段保持 "C:\" 形式（存档树根键）；子目录根用无尾形式（索引键为 Clean 后路径）
  const topSeg = /^[a-zA-Z]:$/.test(top) ? top + '\\' : top;
  const segs = [topSeg];
  let acc = topSeg;
  for (const seg of p.slice(top.length).split('\\')) {
    if (!seg) continue;
    acc = acc + '\\' + seg;
    segs.push(acc);
  }
  return segs;
}

// 面包屑首段标签：盘根显示 "C:"，子目录根显示目录名（GJQMTTest）
function crumbLabel(p) {
  const clean = String(p).replace(/\\+$/, '');
  if (/^[a-zA-Z]:$/.test(clean)) return clean;
  return nodeName(clean);
}

async function loadTreemap(path) {
  const el = $('#treemap');
  if (!el) return; // 视图未挂载（启动早期 ResizeObserver 防抖可能先于模板注入触发）
  state.treemapPath = path;
  state.treemapOthers = null; // 真实路径导航离开"其他"视图
  let kids;
  if (!state.demo) {
    kids = await api().GetChildren(path);
  } else {
    kids = dTree.map(t => ({ path: path.replace(/\\$/, '') + '\\' + t.name, name: t.name, bytes: t.value * 1024 ** 3, files: 0, isDir: true }));
  }
  // 面包屑
  const crumb = $('#tm-crumb');
  $('#tm-title').textContent = `${path} · 方块地图`;
  const segs = crumbSegs(path, state.scanRoots);
  crumb.innerHTML = segs.map((p, i) => {
    const cur = i === segs.length - 1;
    const label = crumbLabel(p);
    return `${i > 0 ? '<span class="sep">›</span>' : ''}<button data-p="${esc(p)}" aria-current="${cur}">${esc(label)}</button>`;
  }).join('');
  crumb.querySelectorAll('button').forEach(b => b.addEventListener('click', () => loadTreemap(b.dataset.p)));

  const box = el;
  const h = box.clientHeight > 300 ? box.clientHeight : 460;
  let othersInfo = null;
  const res = renderTreemap(box, (kids || []).map(k => ({ name: k.name, value: k.bytes, path: k.path })), {
    ramps: RAMPS[curStyle()], fmt: fmtGB, unit: '', height: h, scaleExp: 0.55,
    minSide: 64, othersLabel: '其他',
    onSelect: (p) => loadTreemap(p),
    onOpenOthers: () => { if (othersInfo) loadOthersView(path, othersInfo); },
  });
  othersInfo = (res && res.others) || null;
  renderDirList(path, kids || []);
  if (!kids || kids.length === 0) {
    box.innerHTML = `<div class="empty-inline">此目录没有可下钻的子目录</div>`;
  }
}

// "其他 N 项"专属视图: 被聚合的小项独立成图，脱离大项竞争后全部可读可点
function loadOthersView(basePath, items) {
  const el = $('#treemap');
  if (!el) return; // 视图未挂载
  // items 来自 renderTreemap 的 others 返回值，字段是 {name, value, path}（value 为真实字节）
  const norm = (items || []).map(k => ({ name: k.name, bytes: k.value, path: k.path }));
  state.treemapOthers = { basePath, items: norm };
  const h = el.clientHeight > 300 ? el.clientHeight : 460;
  renderTreemap(el, norm.map(k => ({ name: k.name, value: k.bytes, path: k.path })), {
    ramps: RAMPS[curStyle()], fmt: fmtGB, unit: '', height: h, scaleExp: 0.55,
    onSelect: (p) => loadTreemap(p), // 点击真实目录 → 回到真实路径视图
  });
  const crumb = $('#tm-crumb');
  $('#tm-title').textContent = `${basePath} · 小项 ${items.length} 个`;
  const segs = crumbSegs(basePath, state.scanRoots);
  let html = segs.map((p, i) => `${i > 0 ? '<span class="sep">›</span>' : ''}<button data-p="${esc(p)}">${esc(crumbLabel(p))}</button>`).join('');
  html += `<span class="sep">›</span><button aria-current="true">其他 ${items.length} 项</button>`;
  crumb.innerHTML = html;
  crumb.querySelectorAll('button[data-p]').forEach(b => b.addEventListener('click', () => loadTreemap(b.dataset.p)));
  renderDirList(basePath, norm, `${basePath} · 小项 ${norm.length} 个`);
}

// 当前目录文字列表: 与左侧地图同步，点击行下钻，ⓘ 打开解释抽屉；下方实时文件区支持逐文件删除
function renderDirList(path, kids, titleOverride) {
  const el = $('#dirlist');
  if (!el) return; // 视图未挂载
  $('#dl-title').textContent = titleOverride || `${path} · ${kids.length} 个子目录`;
  const dirHtml = kids.length ? kids.map(k => `
    <div class="dir-row" data-p="${esc(k.path)}">
      ${k.risk ? riskPill(k.risk) : ''}
      <span class="dname" title="${esc(k.name)}">${esc(k.name)}</span>
      ${k.kbName ? `<span class="dkb" title="${esc(k.kbName)}">${esc(k.kbName)}</span>` : ''}
      <span class="dsize num">${fmtGB(k.bytes)}</span>
      <button class="dinfo" data-explain="${esc(k.path)}" title="知识库解释">ⓘ</button>
      <button class="ddel" data-del="${esc(k.path)}" data-name="${esc(k.name)}" data-bytes="${k.bytes || 0}" data-risk="${esc(k.risk || '')}" title="送入回收站">🗑</button>
    </div>`).join('') : `<div class="empty-inline">此目录没有子目录</div>`;
  // 实时文件区：扫描树只存目录，文件按需读取（叶子目录逐文件删除的入口）
  el.innerHTML = dirHtml + `
    <div class="files-head" id="files-head">文件</div>
    <div class="file-rows" id="file-rows"><div class="empty-inline">读取中…</div></div>`;
  el.querySelectorAll('.dir-row').forEach(row => {
    row.addEventListener('click', () => loadTreemap(row.dataset.p));
  });
  el.querySelectorAll('.dinfo').forEach(btn => {
    btn.addEventListener('click', (e) => { e.stopPropagation(); openExplain(btn.dataset.explain); });
  });
  el.querySelectorAll('.ddel').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openDelModal(btn.dataset.del, btn.dataset.name, Number(btn.dataset.bytes) || 0, btn.dataset.risk);
    });
  });
  renderFiles(path);
}

// renderFiles 实时文件区：列出当前目录的文件（按需读取，树里不存文件）；
// token 防 race——快速下钻切换目录后，迟到的旧响应不再写入
let filesToken = 0;
async function renderFiles(path) {
  const rows = $('#file-rows');
  if (!rows || !path || state.demo) return;
  const token = ++filesToken;
  let files = [];
  try {
    files = (await api().ListFiles(path)) || [];
  } catch (e) { files = []; }
  if (token !== filesToken) return; // 目录已切换
  const head = $('#files-head');
  if (head) head.textContent = files.length >= 200 ? `文件 200+ 个（按大小显示前 200）` : `文件 ${files.length} 个`;
  rows.innerHTML = files.map(f => {
    const name = nodeName(f.path);
    return `
    <div class="file-row" title="${esc(f.path)}">
      <span class="dname" title="${esc(name)}">${esc(name)}</span>
      <span class="dsize num">${fmtGB(f.size)}</span>
      <span class="dmtime num">${esc(f.modTime)}</span>
      <button class="ddel" data-del="${esc(f.path)}" data-name="${esc(name)}" data-bytes="${f.size || 0}" data-risk="" title="送入回收站">🗑</button>
    </div>`;
  }).join('') || `<div class="empty-inline">本目录没有文件</div>`;
  rows.querySelectorAll('.ddel').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openDelModal(btn.dataset.del, btn.dataset.name, Number(btn.dataset.bytes) || 0, '');
    });
  });
}

// ---------- 删除确认弹窗（一律送回收站） ----------
let delTarget = null;

async function openDelModal(path, name, bytes, risk) {
  if (!path || state.demo) return;
  delTarget = path;
  $('#del-info').innerHTML = `
    <div class="del-item">
      ${risk ? riskPill(risk) : ''}
      <b>${esc(name)}</b>
      <span class="num" style="color:var(--muted)">${fmtGB(bytes)}</span>
      <div class="del-path">${esc(path)}</div>
    </div>`;
  const errBox = $('#del-error');
  errBox.classList.add('hidden');
  errBox.textContent = '';
  const confirmBtn = $('#del-confirm');
  // 预检未定期间确认按钮必须禁用：弹窗先显示，检查完成才允许点"确认删除"，
  // 杜绝"图标点完没等确认删除就开跑"的竞态
  confirmBtn.disabled = true;
  confirmBtn.textContent = '检查中…';
  $('#del-modal').classList.remove('hidden');
  $('#del-cancel').focus(); // 焦点放取消：误按回车/空格走安全方向
  // 锁定预检：被硬锁的路径提前说明原因，不给点确认的机会
  try {
    const pre = await api().CheckDelete([path]);
    if (pre && pre[0] && !pre[0].ok) {
      errBox.textContent = `无法删除：${pre[0].reason}`;
      errBox.classList.remove('hidden');
      confirmBtn.textContent = '确认删除';
      confirmBtn.disabled = true;
      return;
    }
    confirmBtn.textContent = '确认删除';
    confirmBtn.disabled = false;
  } catch (e) {
    // 预检失败不阻塞（最终删除时后端仍会校验），但同样等本轮结束才放行
    confirmBtn.textContent = '确认删除';
    confirmBtn.disabled = false;
  }
}

function closeDelModal() {
  $('#del-modal').classList.add('hidden');
  delTarget = null;
  const btn = $('#del-confirm');
  btn.disabled = false;
  btn.textContent = '确认删除';
}

async function confirmDelete() {
  const btn = $('#del-confirm');
  if (!delTarget || btn.disabled) return; // 无目标或预检未放行：绝不执行
  btn.disabled = true;
  btn.textContent = '删除中…';
  try {
    const res = await api().DeleteToRecycle([delTarget]);
    const r = res && res[0];
    if (!r || !r.ok) {
      const errBox = $('#del-error');
      errBox.textContent = `删除失败：${(r && r.reason) || '未知原因'}`;
      errBox.classList.remove('hidden');
      btn.textContent = '确认删除';
      return; // 保留弹窗让用户看到原因
    }
    closeDelModal();
    // 刷新当前视图与可清理清单（数据来自内存扫描树，删除的目录大小置零后需重取）
    await loadTreemap(state.treemapPath || state.treemapRoot);
    renderClean(state.treemapRoot);
  } catch (e) {
    const errBox = $('#del-error');
    errBox.textContent = `删除失败：${e}`;
    errBox.classList.remove('hidden');
    btn.textContent = '移入回收站';
  }
}

function nodeName(p) {
  const s = p.replace(/\\$/, '');
  const i = Math.max(s.lastIndexOf('\\'), s.lastIndexOf('/'));
  return i >= 0 ? s.slice(i + 1) : s;
}

// ---------- 增长对比 ----------
async function renderGrowth() {
  const el = $('#growth-chart');
  if (state.demo) {
    Charts.diverging(el, {
      labels: demoGrowth.labels, up: demoGrowth.up, down: demoGrowth.down,
      above: '90 天增长 (GB)', below: '90 天释放 (GB)',
      height: el.clientHeight > 240 ? el.clientHeight : 248,
      format: (v) => (v >= 0 ? '+' : '−') + Math.abs(v).toFixed(1),
    });
    return;
  }
  const scans = await api().ListHistoryScans();
  if (!scans || scans.length < 2) {
    el.innerHTML = `<div class="empty-inline">扫描两次后，这里会显示哪些文件夹长大了</div>`;
    return;
  }
  const deltas = await api().CompareHistory(2, 1, 0.05);
  const top = (deltas || []).slice(0, 8);
  if (!top.length) {
    el.innerHTML = `<div class="empty-inline">两次扫描之间没有明显变化</div>`;
    return;
  }
  const up = [], down = [], labels = [];
  for (const d of top) {
    labels.push(nodeName(d.Path) || d.Path);
    if (d.Diff >= 0) { up.push(+(d.Diff / 1024 ** 3).toFixed(2)); down.push(0); }
    else { up.push(0); down.push(+(Math.abs(d.Diff) / 1024 ** 3).toFixed(2)); }
  }
  Charts.diverging(el, {
    labels, up, down,
    above: '增长 (GB)', below: '释放 (GB)',
    height: el.clientHeight > 240 ? el.clientHeight : 248,
    format: (v) => (v >= 0 ? '+' : '−') + Math.abs(v).toFixed(2),
  });
}

// ---------- 历史视图 ----------
async function renderHistory() {
  const view = $('#view');
  const scans = state.demo ? [] : await api().ListHistoryScans();
  view.innerHTML = `
    <div class="title-row">
      <div><h1>历史</h1><div class="sub">每次扫描自动存档，可对比任意两次看空间变化</div></div>
      <button class="btn primary" id="btn-rescan2">重新扫描</button>
    </div>
    <section class="panel">
      <div class="table-wrap">
        <table>
          <thead><tr><th>#</th><th>时间</th><th>范围</th><th class="n">总计</th><th class="n">耗时</th></tr></thead>
          <tbody id="hist-body"></tbody>
        </table>
      </div>
    </section>
    <section class="panel" style="margin-top:var(--gap);">
      <header>
        <h3 style="font-size:16px;font-weight:600;">对比两次扫描</h3>
        <div style="display:flex;gap:10px;align-items:center;">
          <select class="input" id="cmp-a" style="height:32px;"></select>
          <span style="color:var(--muted)">→</span>
          <select class="input" id="cmp-b" style="height:32px;"></select>
          <button class="btn" id="cmp-go">对比</button>
        </div>
      </header>
      <div id="cmp-result"></div>
    </section>`;
  $('#btn-rescan2').addEventListener('click', openScanModal);

  const body = $('#hist-body');
  body.innerHTML = (scans || []).map(s => `
    <tr><td>${s.index}</td><td class="num">${esc(s.time)}</td>
    <td style="color:var(--muted)">${esc((s.scope || []).join(', '))}</td>
    <td class="n num">${fmtGB(s.total)}</td><td class="n num">${Math.round(s.duration)}s</td></tr>`).join('')
    || `<tr><td colspan="5"><div class="empty-inline">暂无历史，先运行一次扫描</div></td></tr>`;

  const selA = $('#cmp-a'), selB = $('#cmp-b');
  selA.innerHTML = selB.innerHTML = (scans || []).map(s => `<option value="${s.index}">#${s.index} ${esc(s.time)}（${fmtGB(s.total)}）</option>`).join('');
  if (scans && scans.length >= 2) { selA.value = '2'; selB.value = '1'; }
  $('#cmp-go').addEventListener('click', async () => {
    const out = $('#cmp-result');
    out.innerHTML = `<div class="empty-inline">对比中…</div>`;
    const deltas = await api().CompareHistory(+selA.value, +selB.value, 0.05);
    if (!deltas || !deltas.length) { out.innerHTML = `<div class="empty-inline">两次扫描之间没有超过 0.05 GB 的变化</div>`; return; }
    const rows = deltas.slice(0, 15).map(d => `
      <tr><td><span class="truncate">${esc(d.Path)}</span></td>
      <td class="n num">${fmtGB(d.Old)}</td><td class="n num">${fmtGB(d.New)}</td>
      <td class="n num" style="color:${d.Diff >= 0 ? 'var(--warning)' : 'var(--positive)'}">${d.Diff >= 0 ? '+' : '−'}${fmtGB(Math.abs(d.Diff))}</td></tr>`).join('');
    out.innerHTML = `
      <div class="table-wrap"><table>
        <thead><tr><th>路径</th><th class="n">上次</th><th class="n">本次</th><th class="n">变化</th></tr></thead>
        <tbody>${rows}</tbody></table></div>
      ${deltas.length > 15 ? `<div class="meta" style="margin-top:8px;color:var(--muted);font-size:var(--fs-small);">还有 ${deltas.length - 15} 项未展示</div>` : ''}`;
  });
}

// ---------- 设置视图 ----------
async function renderSettings() {
  const view = $('#view');
  const style = curStyle();
  let aiCfg = { provider: 'zhipu', model: '', baseUrl: '', keyMask: '', hasKey: false, presets: [] };
  if (!state.demo && typeof api === 'function') {
    try { aiCfg = await api().GetAIConfig() || aiCfg; } catch (e) { /* 保底默认 */ }
  }
  view.innerHTML = `
    <div class="title-row"><div><h1>设置</h1></div></div>
    <div class="set-section">
      <div class="set-title">外观</div>
      <div class="set-hint">双风格随时切换，记住你的选择</div>
      <div class="style-cards">
        <button class="style-card" data-style="terminal" aria-pressed="${style === 'terminal'}">
          <div class="swatch t-sw"></div><b>终端深色</b><p>白字 + 蓝色选中线 + 等宽数字，仪器感</p>
        </button>
        <button class="style-card" data-style="sheet" aria-pressed="${style === 'sheet'}">
          <div class="swatch f-sw"></div><b>白纸浅色</b><p>白色工作纸 + 青绿点缀，干净亲和</p>
        </button>
      </div>
    </div>
    <div class="set-section">
      <div class="set-title">知识库</div>
      <div class="set-hint">内置 151 条目录知识（用途 / 风险 / 广度连接）。想增改条目，编辑下面的用户扩展文件后重启生效，同 id 会覆盖内置条目。</div>
      <span class="kbd-path" id="kb-path">%APPDATA%\\DiskLens\\kb-user.json</span>
    </div>
    <div class="set-section">
      <div class="set-title">AI 解释</div>
      <div class="set-hint">知识库覆盖不到的目录可交由 AI 解读，仅在点击「测试连接」或「AI 解释」时联网。API Key 经 Windows DPAPI 加密存储，不明文落盘。</div>
      <div class="ai-presets" role="radiogroup">
        ${(aiCfg.presets || []).map(p => `
          <button class="ai-preset" data-p="${esc(p.id)}" aria-pressed="${p.id === aiCfg.provider}">
            <b>${esc(p.name)}</b><span>${esc(p.keyHint)}</span>
          </button>`).join('')}
      </div>
      <div class="ai-form">
        <div class="ai-row" id="ai-url-row" ${aiCfg.provider === 'custom' ? '' : 'hidden'}>
          <label>接口地址（OpenAI 兼容，须 https）</label>
          <input id="ai-url" type="text" placeholder="https://your-endpoint.example.com/v1" value="${esc(aiCfg.baseUrl || '')}">
        </div>
        <div class="ai-row">
          <label>API Key ${aiCfg.hasKey ? `<span class="ai-masked">已配置 ${esc(aiCfg.keyMask)}，留空则保留</span>` : ''}</label>
          <input id="ai-key" type="password" autocomplete="off" placeholder="${aiCfg.hasKey ? '留空保留现有 Key' : '粘贴 API Key'}">
        </div>
        <div class="ai-row">
          <label>模型</label>
          <input id="ai-model" type="text" value="${esc(aiCfg.model || '')}" placeholder="如 glm-4-flash / deepseek-chat">
        </div>
        <div class="ai-actions">
          <button class="btn" id="ai-save">保存配置</button>
          <button class="btn" id="ai-test">测试连接</button>
          <span id="ai-status" class="ai-status"></span>
        </div>
      </div>
    </div>
    <div class="set-section">
      <div class="set-title">隐私与安全</div>
      <div class="set-hint">除 AI 功能（仅你主动触发）外，本工具没有任何联网行为：扫描、存档、知识库全部本地完成。系统路径（Windows / Program Files / ProgramData 等）被硬编码锁定，任何操作都无法删除。</div>
    </div>`;
  view.querySelectorAll('.style-card').forEach(card => {
    card.addEventListener('click', () => {
      const s = card.dataset.style;
      document.documentElement.dataset.style = s;
      localStorage.setItem('dl.style', s);
      view.querySelectorAll('.style-card').forEach(c => c.setAttribute('aria-pressed', c === card));
    });
  });
  // AI 配置区交互
  const status = (msg, ok) => {
    const el = $('#ai-status');
    if (el) { el.textContent = msg; el.classList.toggle('ai-ok', !!ok); el.classList.toggle('ai-err', !ok); }
  };
  const curProvider = () => view.querySelector('.ai-preset[aria-pressed="true"]')?.dataset.p || 'zhipu';
  const presetOf = id => (aiCfg.presets || []).find(p => p.id === id);
  view.querySelectorAll('.ai-preset').forEach(card => {
    card.addEventListener('click', () => {
      view.querySelectorAll('.ai-preset').forEach(c => c.setAttribute('aria-pressed', c === card));
      const p = presetOf(card.dataset.p);
      const urlRow = $('#ai-url-row');
      if (urlRow) urlRow.hidden = card.dataset.p !== 'custom';
      if (p && $('#ai-model')) $('#ai-model').value = p.defaultModel || ''; // 换家自动填默认模型，可再改
    });
  });
  // 保存（可选先保存）后刷新设置页状态区
  const doSave = async () => {
    const provider = curProvider();
    const apiKey = $('#ai-key').value.trim();
    const model = $('#ai-model').value.trim();
    const baseURL = $('#ai-url') ? $('#ai-url').value.trim() : '';
    await api().SaveAIConfig(provider, apiKey, model, baseURL);
    $('#ai-key').value = '';
    await renderSettings(); // 重新渲染以更新"已配置 sk-x****"与隐藏 url 行
  };
  $('#ai-save')?.addEventListener('click', async () => {
    status('保存中…', true);
    try { await doSave(); status('已保存', true); }
    catch (e) { status('保存失败: ' + e, false); }
  });
  $('#ai-test')?.addEventListener('click', async () => {
    status('测试中（联网请求模型）…', true);
    try { await doSave(); } catch (e) { status('保存失败: ' + e, false); return; }
    try {
      const out = await api().TestAI();
      status('✓ 连接成功：' + String(out || '').slice(0, 40), true);
    } catch (e) {
      status('✗ ' + e, false);
    }
  });
}

// ---------- 解释抽屉 ----------
async function openExplain(path) {
  if (!path) return;
  const body = $('#drawer-body');
  $('#drawer-backdrop').classList.add('show');
  const drawer = $('#explain-drawer');
  drawer.classList.add('open');
  drawer.setAttribute('aria-hidden', 'false');
  body.innerHTML = `<div class="empty-inline">读取知识库…</div>`;
  let hits;
  if (!state.demo) {
    hits = await api().ExplainPath(path);
  } else {
    hits = [{
      entryId: 'demo', name: '演示条目', cat: '演示', risk: 'caution',
      desc: `这是「${path}」的演示解释（浏览器预览模式）`, why: '在 disklens.exe 里运行可看到 151 条真实知识库条目的解释。',
      exists: [path], sizes: {}, clean: [], related: [],
    }];
  }
  if (!hits || !hits.length) {
    body.innerHTML = `
      <div class="ex-block"><div class="ex-head"><b>${esc(nodeName(path))}</b></div>
      <div class="ex-desc">知识库还没有这个目录的条目。</div>
      <div class="ex-why">可在 %APPDATA%\\DiskLens\\kb-user.json 里添加自定义条目，或让 AI 解读。</div>
      ${state.demo ? '' : `<div class="ex-label">本机路径</div>
      <ul class="ex-paths"><li><span class="p" title="${esc(path)}">${esc(path)}</span><button class="ex-open" data-open="${esc(path)}" title="在资源管理器中打开">打开</button></li></ul>`}</div>
      ${aiBlockHTML(path)}`;
    bindExOpen(body);
    bindAIBlock(body, path);
    return;
  }
  body.innerHTML = hits.map((h, i) => {
    // 落盘列表 = 请求路径（置顶，含 __self 大小） ∪ 知识库命中的其他落盘点
    const seen = new Set();
    const rows = [];
    const selfSize = h.sizes && h.sizes['__self'];
    rows.push({ p: path, size: typeof selfSize === 'number' ? selfSize : null, self: true });
    seen.add(String(path).toLowerCase());
    for (const p of (h.exists || [])) {
      if (seen.has(String(p).toLowerCase())) continue;
      seen.add(String(p).toLowerCase());
      rows.push({ p, size: h.sizes && h.sizes[p] ? h.sizes[p] : null });
    }
    return `
    <div class="ex-block">
      <div class="ex-head"><b>${esc(h.name)}</b>${riskPill(h.risk)}<span class="pill" style="background:var(--pill-neutral-bg);color:var(--pill-neutral-ink);">${esc(h.cat)}</span></div>
      <div class="ex-desc">${esc(h.desc)}</div>
      <div class="ex-why">${esc(h.why)}</div>
      ${rows.length ? `<div class="ex-label">本机落盘点（广度连接）</div>
        <ul class="ex-paths">${rows.map(r => `
          <li><span class="p" title="${esc(r.p)}">${esc(r.p)}</span><span class="s num">${r.size ? fmtGB(r.size) : ''}</span>${state.demo ? '' : `<button class="ex-open" data-open="${esc(r.p)}" title="在资源管理器中打开">打开</button>`}</li>`).join('')}</ul>` : ''}
      ${(h.clean || []).length ? `<div class="ex-label">可清理子项</div>
        <ul class="ex-clean">${h.clean.map(c => `<li>${esc(c.sub)} ${riskPill(c.risk)}<br><span style="color:var(--muted);font-size:var(--fs-small);">${esc(c.note)}</span></li>`).join('')}</ul>` : ''}
      ${(h.related || []).map(r => `<div class="ex-label">关联：${esc(r.note || '相关位置')}</div>
        <ul class="ex-paths">${(r.paths || []).map(p => `<li><span class="p">${esc(p)}</span><button class="ex-open" data-open="${esc(p)}" title="在资源管理器中打开">打开</button></li>`).join('')}</ul>`).join('')}
    </div>`;
  }).join('');
  body.insertAdjacentHTML('beforeend', aiBlockHTML(path));
  bindExOpen(body);
  bindAIBlock(body, path);
}

// bindExOpen 抽屉内「打开」按钮统一绑定
function bindExOpen(scope) {
  scope.querySelectorAll('.ex-open').forEach(btn => {
    btn.addEventListener('click', async (e) => {
      e.stopPropagation();
      btn.disabled = true;
      try { await api().OpenFolder(btn.dataset.open); }
      catch (err) { console.warn('打开失败:', err); }
      finally { btn.disabled = false; }
    });
  });
}

// ---------- AI 补充解读（抽屉内，仅点击时联网） ----------
function aiBlockHTML(path) {
  if (state.demo) return '';
  return `
  <div class="ex-block ai-block">
    <div class="ex-label">AI 补充解读</div>
    <div id="ai-body">
      <button class="btn ai-ask" id="ai-ask">🤖 让 AI 解读这个目录</button>
      <div class="ai-note">需先在「设置 → AI 解释」配置 API Key；点击才会联网。</div>
    </div>
  </div>`;
}

// bindAIBlock 点击后收集目录上下文调用 ExplainWithAI，结果就地渲染
function bindAIBlock(scope, path) {
  const btn = scope.querySelector('#ai-ask');
  if (!btn) return;
  btn.addEventListener('click', async () => {
    const box = scope.querySelector('#ai-body');
    if (!box) return;
    btn.disabled = true;
    box.innerHTML = `<div class="empty-inline">正在询问 AI…（首次调用可能需要数秒）</div>`;
    try {
      const r = await api().ExplainWithAI(path);
      box.innerHTML = `
        <div class="ai-text">${aiMarkdown(r.text)}</div>
        <div class="ai-sign">由 ${esc(r.model || 'AI')} 生成 · AI 判断仅供参考，删除前请自行确认</div>`;
    } catch (e) {
      const msg = String(e || '');
      box.innerHTML = `
        <div class="ai-err">${esc(msg)}</div>
        ${/尚未配置|Key/.test(msg) ? `<button class="btn" id="ai-goto-settings">去设置页配置</button>` : ''}
        <button class="btn" id="ai-retry">重试</button>`;
      const go = box.querySelector('#ai-goto-settings');
      if (go) go.addEventListener('click', () => { closeExplain(); switchView('settings'); });
      box.querySelector('#ai-retry')?.addEventListener('click', () => {
        btn.disabled = false;
        btn.click();
      });
    }
  });
}

// aiMarkdown 轻量渲染：按行分段，【标题】行加粗，短横线行成列表
function aiMarkdown(text) {
  const lines = String(text || '').split(/\r?\n/).filter(l => l.trim());
  return lines.map(l => {
    const t = esc(l.trim());
    if (/^【.+】/.test(l.trim())) return `<div class="ai-h">${t}</div>`;
    if (/^[-•·]\s/.test(l.trim())) return `<div class="ai-li">${t.replace(/^[-•·]\s/, '· ')}</div>`;
    return `<div class="ai-p">${t}</div>`;
  }).join('');
}

function closeExplain() {
  $('#drawer-backdrop').classList.remove('show');
  const drawer = $('#explain-drawer');
  drawer.classList.remove('open');
  drawer.setAttribute('aria-hidden', 'true');
}

// ---------- 扫描弹窗 ----------
let scanCustom = []; // 会话内保留，便于重复扫描同一组子文件夹

async function openScanModal() {
  $('#scan-error').classList.add('hidden');
  $('#scan-bar').classList.add('hidden');
  $('#scan-meta').classList.add('hidden');
  $('#scan-start').disabled = false;
  const wrap = $('#scan-drives');
  wrap.innerHTML = `<div class="empty-inline">读取磁盘列表…</div>`;
  $('#scan-modal').classList.remove('hidden');
  renderScanCustom();
  if (state.demo) {
    wrap.innerHTML = `<div class="empty-inline">浏览器演示模式不支持扫描，请在 disklens.exe 中运行</div>`;
    $('#scan-start').disabled = true;
    return;
  }
  const drives = await api().GetDrives();
  wrap.innerHTML = (drives || []).map(d => `
    <label class="drive-option">
      <input type="checkbox" value="${esc(d.letter)}" checked>
      <b>${esc(d.letter)}</b>
      <span class="meta num">总 ${fmtGB(d.total)} · 剩 ${fmtGB(d.free)}</span>
    </label>`).join('') || `<div class="empty-inline">未找到固定磁盘</div>`;
}

function scanErr(msg) {
  const err = $('#scan-error');
  err.textContent = msg;
  err.classList.remove('hidden');
}

function renderScanCustom() {
  const list = $('#scan-custom-list');
  if (!list) return;
  list.innerHTML = scanCustom.map((p, i) => `
    <div class="scan-custom-item">
      <span class="scan-custom-path" title="${esc(p)}">${esc(p)}</span>
      <button class="scan-custom-del" data-i="${i}" title="移除">✕</button>
    </div>`).join('');
  list.querySelectorAll('.scan-custom-del').forEach(b => b.addEventListener('click', () => {
    scanCustom.splice(+b.dataset.i, 1);
    renderScanCustom();
  }));
}

function addScanCustom(raw) {
  let p = (raw || '').trim().replace(/^"+|"+$/g, '').replace(/\//g, '\\');
  if (!p) return;
  if (!/^[a-zA-Z]:\\/.test(p)) {
    scanErr('请输入完整路径（含盘符，如 D:\\下载）');
    return;
  }
  p = p.replace(/\\+$/, '') || p; // 去尾反斜杠（保留盘根 C:\ 本身）
  if (scanCustom.some(x => x.toLowerCase() === p.toLowerCase())) return;
  // 子文件夹若落在已勾选盘内 → 自动取消勾选该盘，避免整盘+子树重复扫描
  const letter = p.slice(0, 2) + '\\';
  const cb = document.querySelector(`#scan-drives input[value="${CSS.escape(letter)}"]`);
  if (cb && cb.checked) cb.checked = false;
  scanCustom.push(p);
  $('#scan-error').classList.add('hidden');
  renderScanCustom();
}

async function startScan() {
  const checked = [...document.querySelectorAll('#scan-drives input:checked')].map(i => i.value);
  const roots = [...checked, ...scanCustom];
  if (!roots.length) {
    scanErr('至少勾选一个磁盘，或添加要扫描的文件夹');
    return;
  }
  $('#scan-start').disabled = true;
  $('#scan-error').classList.add('hidden');
  $('#scan-bar').classList.remove('hidden');
  $('#scan-bar').classList.add('indet');
  $('#scan-meta').classList.remove('hidden');
  $('#scan-meta').textContent = '正在扫描…';
  await api().StartScan(roots);
}

function wireScanEvents() {
  if (state.demo || typeof window.runtime === 'undefined') return;
  window.runtime.EventsOn('scan:progress', (p) => {
    const meta = $('#scan-meta');
    if (meta) meta.textContent = `已扫描 ${comma(p.dirs)} 目录 · ${comma(p.files)} 文件 · ${fmtGB(p.bytes)}`;
  });
  window.runtime.EventsOn('scan:done', async (r) => {
    $('#scan-meta').textContent = `完成：${Math.round(r.durationSec)} 秒 · ${r.errors} 处无法访问已跳过`;
    $('#scan-bar').classList.remove('indet');
    $('#scan-bar').querySelector('div').style.width = '100%';
    setTimeout(() => {
      $('#scan-modal').classList.add('hidden');
      state.overview = null;
      if (state.view === 'overview') renderOverview();
    }, 700);
  });
  window.runtime.EventsOn('scan:error', (msg) => {
    const err = $('#scan-error');
    err.textContent = '扫描失败: ' + msg;
    err.classList.remove('hidden');
    $('#scan-start').disabled = false;
  });
}

function demoOverview() {
  return {
    ready: true, durationSec: 0,
    drives: [
      { letter: 'C:\\', total: 952.7 * 1024 ** 3, free: 219.8 * 1024 ** 3, usedPc: 77.2, atRisk: false },
      { letter: 'D:\\', total: 476.9 * 1024 ** 3, free: 49.1 * 1024 ** 3, usedPc: 89.7, atRisk: true },
    ],
    finding: 'D 盘仅剩 49.1 GB（10.3%）—— 微信文件 90 天 +18.3 GB，是最大增长源（演示数据）',
    finishedAt: '10-28 21:40',
    topFiles: demoTop.map(f => ({ path: f.name, size: f.size * 1024 ** 3, modTime: f.mtime })),
    typeStats: [
      { category: '视频', bytes: 38.5 * 1024 ** 3, files: 564 },
      { category: '压缩包', bytes: 53.4 * 1024 ** 3, files: 2985 },
      { category: '文档', bytes: 4.5 * 1024 ** 3, files: 200 },
    ],
  };
}

// ---------- 错误浮层（前端异常不再静默） ----------
function showFatal(msg) {
  let bar = $('#fatal-bar');
  if (!bar) {
    bar = document.createElement('div');
    bar.id = 'fatal-bar';
    bar.style.cssText = 'position:fixed;left:12px;bottom:12px;right:12px;max-height:40vh;overflow:auto;z-index:99;background:var(--pill-negative-bg);color:var(--pill-negative-ink);border:1px solid var(--negative);border-radius:8px;padding:10px 14px;font-size:12px;font-family:var(--font-num);white-space:pre-wrap;';
    document.body.appendChild(bar);
  }
  bar.textContent += '[前端错误] ' + msg + '\n';
}
window.addEventListener('error', (e) => showFatal((e.message || '') + '\n' + (e.error && e.error.stack ? e.error.stack.split('\n').slice(0, 4).join('\n') : (e.filename || '') + ':' + (e.lineno || ''))));
window.addEventListener('unhandledrejection', (e) => {
  const r = e.reason;
  showFatal(String(r) + (r && r.stack ? '\n' + r.stack.split('\n').slice(0, 4).join('\n') : ''));
});

// ---------- 启动 ----------
async function boot() {
  document.querySelectorAll('.nav-item').forEach(a => a.addEventListener('click', (e) => { e.preventDefault(); switchView(a.dataset.view); }));
  $('#drawer-close').addEventListener('click', closeExplain);
  $('#drawer-backdrop').addEventListener('click', closeExplain);
  $('#scan-cancel').addEventListener('click', () => $('#scan-modal').classList.add('hidden'));
  $('#scan-start').addEventListener('click', startScan);
  $('#scan-add').addEventListener('click', () => addScanCustom($('#scan-custom-input').value));
  $('#scan-custom-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') addScanCustom($('#scan-custom-input').value);
  });
  $('#scan-browse').addEventListener('click', async () => {
    try {
      const p = await api().SelectDirectory();
      if (p) addScanCustom(p);
    } catch (e) { scanErr('打开目录选择器失败: ' + e); }
  });
  $('#del-cancel').addEventListener('click', closeDelModal);
  $('#del-confirm').addEventListener('click', confirmDelete);
  wireScanEvents();
  // 真实模式：无内存数据时先尝试自动恢复最近一次存档
  if (!state.demo) {
    try {
      const ov = await api().GetOverview();
      if (!ov.ready) {
        await api().LoadLatestScan();
      }
    } catch (err) {
      showFatal('启动恢复失败: ' + String(err));
    }
  }
  switchView('overview');
}
document.fonts?.ready?.then(boot) ?? boot();
