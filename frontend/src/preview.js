// 盘镜 DiskLens — M0 预览页逻辑: 渲染样例数据 + 风格切换
import { drives, dTree, cleanItems, cleanDirect, cleanSystemOp, growth, topFiles } from './data.js';
import { renderTreemap } from './treemap.js';

const $ = (sel) => document.querySelector(sel);
const fmtGB = (v) => v >= 100 ? v.toFixed(1) : v.toFixed(1);

// treemap 色阶: 同族阶梯（terminal=蓝系暗阶 / sheet=teal 系浅阶），主数据元素带色相 → no-data-hue 达标
const RAMPS = {
  terminal: ['#2a425e', '#243a52', '#1f3246', '#1a2a3b', '#152231', '#111a26', '#0d141d', '#0a1017', '#080c11'],
  sheet: ['#cfe3e8', '#bdd8df', '#accdd6', '#9bc2cd', '#8ab7c4', '#79acb9', '#68a1af', '#5796a4', '#468b99'],
};

function currentStyle() {
  return document.documentElement.dataset.style || 'terminal';
}

function renderDrives() {
  const wrap = $('#drives');
  wrap.innerHTML = Object.entries(drives).map(([k, d]) => {
    const used = +(d.total - d.free).toFixed(1);
    const pct = Math.round((used / d.total) * 1000) / 10;
    const ticks = [25, 50, 75].map(p => `<span class="water-tick" style="left:${p}%"></span>`).join('');
    return `
      <div class="kpi">
        <div class="kpi-label">${d.label}</div>
        <div class="kpi-value">${fmtGB(d.free)} <small>GB 可用 · ${pct}% 已用</small></div>
        <div class="water">
          <div class="water-track">${ticks}<div class="water-fill${d.atRisk ? ' at-risk' : ''}" style="width:${pct}%"></div></div>
          <div class="water-ends mono">
            <span>已用 ${fmtGB(used)} GB</span>
            <span class="free${d.atRisk ? ' at-risk' : ''}">剩 ${fmtGB(d.free)} GB</span>
          </div>
        </div>
      </div>`;
  }).join('');
}

function renderClean() {
  const badge = { safe: '<span class="pill pos">安全</span>', caution: '<span class="pill warn">需确认</span>', system: '<span class="pill">需系统操作</span>' };
  $('#clean-rows').innerHTML = cleanItems.map(i => `
    <div class="clean-row">
      ${badge[i.risk]}
      <div class="cname">${i.name}</div>
      <div class="csize num">${i.size.toFixed(1)} <small style="color:var(--muted)">GB</small></div>
      <div class="creason">${i.reason} · ${i.where}</div>
    </div>`).join('');
}

function renderTopFiles() {
  $('#topfiles-body').innerHTML = topFiles.map(f => `
    <tr>
      <td><span class="truncate">${f.name}</span><span class="sub">${f.where}</span></td>
      <td class="n num">${f.size.toFixed(1)} <small style="color:var(--muted)">GB</small></td>
      <td class="n num" style="color:var(--muted)">${f.mtime}</td>
    </tr>`).join('');
}

function renderGrowth() {
  const el = $('#growth-chart');
  const res = Charts.diverging(el, {
    labels: growth.labels,
    up: growth.up,
    down: growth.down,
    above: '90 天增长 (GB)',
    below: '90 天释放 (GB)',
    format: (v) => (v >= 0 ? '+' : '−') + Math.abs(v).toFixed(1),
  });
  if (res && res.legend) $('#growth-legend').innerHTML = '';
}

function renderTreemapNow() {
  renderTreemap($('#treemap'), dTree, { ramps: RAMPS[currentStyle()], fmt: fmtGB });
}

function boot() {
  renderDrives();
  renderClean();
  renderTopFiles();
  renderGrowth();
  renderTreemapNow();

  document.querySelectorAll('.style-switch button').forEach(btn => {
    btn.addEventListener('click', () => {
      document.documentElement.dataset.style = btn.dataset.style;
      document.querySelectorAll('.style-switch button').forEach(b => b.setAttribute('aria-pressed', b === btn));
      renderTreemapNow();
      $('#growth-chart').innerHTML = '';
      renderGrowth();
    });
  });
}
document.fonts?.ready?.then(boot) ?? boot();
