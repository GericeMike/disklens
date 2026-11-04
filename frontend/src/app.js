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
    <div class="grid-mid">
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;" id="tm-title">方块地图</h3>
        <span class="meta">点击方块下钻 · 悬停查看大小</span></header>
        <div class="crumbbar" id="tm-crumb"></div>
        <div class="panel-body" id="treemap"></div>
      </section>
      <section class="panel">
        <header><h3 style="font-size:16px;font-weight:600;">可清理项</h3><span class="meta num" id="clean-total"></span></header>
        <div class="panel-body" id="clean-rows"></div>
      </section>
    </div>
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
  if (!d.ready) {
    $('#drives').innerHTML = `<div class="empty" style="grid-column:1/-1;">还没有扫描数据 —— 点击「重新扫描」开始</div>`;
    $('#treemap').innerHTML = '';
    $('#clean-rows').innerHTML = `<div class="empty-inline">扫描后这里会给出知识库判定的可清理清单</div>`;
    $('#growth-chart').innerHTML = `<div class="empty-inline">需要两次扫描才能对比</div>`;
    $('#topfiles-body').innerHTML = '';
    return;
  }

  // treemap: 默认展示占用最大盘的根；盘概览水位条兼任 treemap 的盘切换标签
  const biggest = [...(d.drives || [])].sort((a, b) => (b.total - b.free) - (a.total - a.free))[0];
  const rootPath = biggest ? biggest.letter : 'C:\\';
  if (!state.treemapRoot) {
    state.treemapRoot = rootPath;
  }
  renderDrives(d.drives, state.treemapRoot);
  renderTopFiles(d.topFiles);
  renderTypes(d.typeStats);
  renderClean();
  await loadTreemap(state.treemapRoot);
  renderGrowth();
}

function renderDrives(drives, selected) {
  const wrap = $('#drives');
  // 盘概览兼任 treemap 的盘切换标签（§5: KPI 作为切换控件，选中项带标记线）
  wrap.setAttribute('role', 'tablist');
  wrap.innerHTML = (drives || []).map(d => {
    const used = d.total - d.free;
    const pct = Number.isFinite(d.usedPct) ? d.usedPct : 0;
    const ticks = [25, 50, 75].map(p => `<span class="water-tick" style="left:${p}%"></span>`).join('');
    const sel = d.letter === selected;
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
      if (!d || state.treemapRoot === d.letter) return;
      state.treemapRoot = d.letter;
      wrap.querySelectorAll('.kpi').forEach((c, j) => c.setAttribute('aria-selected', j === i ? 'true' : 'false'));
      wrap.querySelectorAll('.kpi-label').forEach((l, j) => {
        l.textContent = `${drives[j].letter} 磁盘${j === i ? ' · 正在查看' : ''}`;
      });
      loadTreemap(d.letter);
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

async function renderClean() {
  const el = $('#clean-rows');
  let items;
  if (!state.demo) {
    items = await api().GetCleanSummary();
  } else {
    items = demoClean.map((c, i) => ({
      entryId: 'demo' + i, name: c.name, risk: c.risk, why: c.reason,
      totalSize: c.size * 1024 ** 3, paths: [{ path: c.where, size: c.size * 1024 ** 3 }],
    }));
  }
  const total = (items || []).filter(i => i.risk === 'safe').reduce((s, i) => s + i.totalSize, 0);
  $('#clean-total').textContent = items && items.length ? `🟢项合计 ${fmtGB(total)}` : '';
  el.innerHTML = (items || []).slice(0, 8).map(i => {
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
async function loadTreemap(path) {
  state.treemapPath = path;
  const el = $('#treemap');
  let kids;
  if (!state.demo) {
    kids = await api().GetChildren(path);
  } else {
    kids = dTree.map(t => ({ path: path.replace(/\\$/, '') + '\\' + t.name, name: t.name, bytes: t.value * 1024 ** 3, files: 0, isDir: true }));
  }
  // 面包屑
  const crumb = $('#tm-crumb');
  $('#tm-title').textContent = `${path} · 方块地图`;
  const segs = [];
  if (/^[a-zA-Z]:\\?$/.test(path)) { segs.push(path); }
  else {
    let acc = '';
    for (const seg of path.split('\\')) {
      if (seg === '') { acc = acc || ''; continue; }
      acc = acc ? acc + '\\' + seg : seg + '\\';
      segs.push(acc.endsWith(':\\') ? acc : acc);
    }
  }
  crumb.innerHTML = segs.map((p, i) => {
    const cur = i === segs.length - 1;
    const label = i === 0 ? p.replace(/\\$/, '') : nodeName(p);
    return `${i > 0 ? '<span class="sep">›</span>' : ''}<button data-p="${esc(p)}" aria-current="${cur}">${esc(label)}</button>`;
  }).join('');
  crumb.querySelectorAll('button').forEach(b => b.addEventListener('click', () => loadTreemap(b.dataset.p)));

  const box = el;
  const h = box.clientHeight > 300 ? box.clientHeight : 360;
  renderTreemap(box, (kids || []).map(k => ({ name: k.name, value: k.bytes, path: k.path })), {
    ramps: RAMPS[curStyle()], fmt: fmtGB, unit: '', height: h,
    onSelect: (p) => loadTreemap(p),
  });
  if (!kids || kids.length === 0) {
    box.innerHTML = `<div class="empty-inline">此目录没有可下钻的子目录</div>`;
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
function renderSettings() {
  const view = $('#view');
  const style = curStyle();
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
      <div class="set-title">AI 解释（即将推出）</div>
      <div class="set-hint">内置知识库覆盖不到的目录将可交由 AI 解读；支持智谱 / DeepSeek / Kimi / 自定义 OpenAI 兼容接口，仅在你主动触发时联网。</div>
    </div>
    <div class="set-section">
      <div class="set-title">隐私与安全</div>
      <div class="set-hint">除 AI 功能（未启用）外，本工具没有任何联网行为：扫描、存档、知识库全部本地完成。系统路径（Windows / Program Files / ProgramData 等）被硬编码锁定，任何操作都无法删除。</div>
    </div>`;
  view.querySelectorAll('.style-card').forEach(card => {
    card.addEventListener('click', () => {
      const s = card.dataset.style;
      document.documentElement.dataset.style = s;
      localStorage.setItem('dl.style', s);
      view.querySelectorAll('.style-card').forEach(c => c.setAttribute('aria-pressed', c === card));
    });
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
      <div class="ex-why">可在 %APPDATA%\\DiskLens\\kb-user.json 里添加自定义条目；AI 解释功能上线后会自动兜底。</div></div>`;
    return;
  }
  body.innerHTML = hits.map((h, i) => `
    <div class="ex-block">
      <div class="ex-head"><b>${esc(h.name)}</b>${riskPill(h.risk)}<span class="pill" style="background:var(--pill-neutral-bg);color:var(--pill-neutral-ink);">${esc(h.cat)}</span></div>
      <div class="ex-desc">${esc(h.desc)}</div>
      <div class="ex-why">${esc(h.why)}</div>
      ${(h.exists || []).length ? `<div class="ex-label">本机落盘点（广度连接）</div>
        <ul class="ex-paths">${h.exists.map(p => `
          <li><span class="p">${esc(p)}</span><span class="s num">${h.sizes && h.sizes[p] ? fmtGB(h.sizes[p]) : ''}</span></li>`).join('')}</ul>` : ''}
      ${(h.clean || []).length ? `<div class="ex-label">可清理子项</div>
        <ul class="ex-clean">${h.clean.map(c => `<li>${esc(c.sub)} ${riskPill(c.risk)}<br><span style="color:var(--muted);font-size:var(--fs-small);">${esc(c.note)}</span></li>`).join('')}</ul>` : ''}
      ${(h.related || []).map(r => `<div class="ex-label">关联：${esc(r.note || '相关位置')}</div>
        <ul class="ex-paths">${(r.paths || []).map(p => `<li><span class="p">${esc(p)}</span></li>`).join('')}</ul>`).join('')}
    </div>`).join('');
}

function closeExplain() {
  $('#drawer-backdrop').classList.remove('show');
  const drawer = $('#explain-drawer');
  drawer.classList.remove('open');
  drawer.setAttribute('aria-hidden', 'true');
}

// ---------- 扫描弹窗 ----------
async function openScanModal() {
  $('#scan-error').classList.add('hidden');
  $('#scan-bar').classList.add('hidden');
  $('#scan-meta').classList.add('hidden');
  $('#scan-start').disabled = false;
  const wrap = $('#scan-drives');
  wrap.innerHTML = `<div class="empty-inline">读取磁盘列表…</div>`;
  $('#scan-modal').classList.remove('hidden');
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

async function startScan() {
  const checked = [...document.querySelectorAll('#scan-drives input:checked')].map(i => i.value);
  if (!checked.length) {
    const err = $('#scan-error');
    err.textContent = '至少选择一个磁盘';
    err.classList.remove('hidden');
    return;
  }
  $('#scan-start').disabled = true;
  $('#scan-error').classList.add('hidden');
  $('#scan-bar').classList.remove('hidden');
  $('#scan-bar').classList.add('indet');
  $('#scan-meta').classList.remove('hidden');
  $('#scan-meta').textContent = '正在扫描…';
  await api().StartScan(checked);
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
