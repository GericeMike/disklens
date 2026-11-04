// 盘镜 DiskLens — squarified treemap（Bruls et al. 标准算法; §6 规则自绘 SVG:
// 方角、1px 描边、无渐变、悬停 1px accent 线、面积足够才放标签）
// 交互目标对齐 WinDirStat/ECharts treemap，实现零依赖。
export function squarify(items, x, y, w, h) {
  // items: [{name, value}] 已按 value 降序; 返回 [{name, value, x, y, w, h}]
  const total = items.reduce((s, d) => s + d.value, 0);
  const scale = (w * h) / total;
  const rects = [];
  let rx = x, ry = y, rw = w, rh = h;
  let row = [], rowArea = 0; // rowArea 为缩放后面积

  function worst(r, area, sideLen) {
    const thick = area / sideLen;
    let bad = 0;
    for (const t of r) {
      const len = (t.value * scale) / thick;
      const ratio = Math.max(thick / len, len / thick);
      if (ratio > bad) bad = ratio;
    }
    return bad;
  }

  function layoutRow() {
    const sideLen = Math.min(rw, rh);
    const thick = rowArea / sideLen;
    let off = 0;
    for (const it of row) {
      const len = (it.value * scale) / thick;
      if (rw >= rh) rects.push({ ...it, x: rx, y: ry + off, w: thick, h: len });
      else rects.push({ ...it, x: rx + off, y: ry, w: len, h: thick });
      off += len;
    }
    if (rw >= rh) { rx += thick; rw -= thick; } else { ry += thick; rh -= thick; }
    row = []; rowArea = 0;
  }

  for (const it of items) {
    const area = it.value * scale;
    const sideLen = Math.min(rw, rh);
    const withNew = row.length ? worst([...row, it], rowArea + area, sideLen) : Infinity;
    if (row.length === 0 || withNew <= worst(row, rowArea, sideLen)) {
      row.push(it); rowArea += area;
    } else {
      layoutRow();
      row.push(it); rowArea = area;
    }
  }
  if (row.length) layoutRow();
  return rects;
}

// 渲染到容器: theme ramp 由调用方传入（深/浅两套色阶，蓝/teal 同族阶梯）
// items: [{name, value, path?}]；onSelect(path) 提供时点击方块即下钻回调
// scaleExp: 面积缓比例指数（0.55 = 开根号式压缩，避免超大项独占半屏、小项不可读；
//           排序仍按真实值从大到小，标签/悬浮提示显示真实大小）
// minSide: 尾部聚合阈值(px)。缩放后不足 minSide² 的连续尾部合并为一个"其他"方块，
//          面积保底可点击；点击触发 onOpenOthers()，由调用方展开"其他"专属视图。
//          返回值 { others } 携带被聚合的原始条目。
export function renderTreemap(el, items, { ramps, fmt, unit = '', height = null, onSelect = null, scaleExp = 1, minSide = 0, othersLabel = '其他', onOpenOthers = null }) {
  const W = Math.max(320, el.clientWidth || 800);
  const H = Math.max(280, height || 360);
  const sorted = [...items].sort((a, b) => b.value - a.value);
  if (scaleExp !== 1) {
    for (const it of sorted) it._scaled = Math.pow(Math.max(0, it.value), scaleExp);
  }
  const scaled = scaleExp !== 1
    ? sorted.map(it => ({ ...it, value: it._scaled }))
    : sorted;

  // 尾部聚合: 找到第一个缩放面积不足 minSide² 的位置，其后全部并入"其他"
  let others = null;
  let layout = scaled;
  if (minSide > 0 && scaled.length > 0) {
    const totalScaled = scaled.reduce((s, d) => s + d.value, 0);
    if (totalScaled > 0) {
      const unitPx = ((W - 1) * (H - 1)) / totalScaled;
      const minArea = minSide * minSide;
      let cut = -1;
      for (let i = 0; i < scaled.length; i++) {
        if (scaled[i].value * unitPx < minArea) { cut = i; break; }
      }
      if (cut >= 0 && cut < scaled.length) {
        others = sorted.slice(cut);
        const groupValue = (minArea * 1.15) / unitPx;
        layout = scaled.slice(0, cut).concat([{
          name: othersLabel, value: groupValue,
          _group: true, _count: others.length, _items: others,
        }]);
      }
    }
  }
  const rects = squarify(layout, 0.5, 0.5, W - 1, H - 1);
  const n = ramps.length;
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  svg.setAttribute('width', W); svg.setAttribute('height', H);
  svg.setAttribute('class', 'treemap-svg');
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', '目录占用方块地图');
  for (let i = 0; i < rects.length; i++) {
    const r = rects[i];
    const g = document.createElementNS(ns, 'g');
    if (r._group && onOpenOthers) {
      // "其他"聚合方块：点击展开小项专属视图
      g.style.cursor = 'pointer';
      g.addEventListener('click', () => onOpenOthers());
    } else if (onSelect && r.path) {
      g.style.cursor = 'pointer';
      g.addEventListener('click', () => onSelect(r.path));
    }
    const rect = document.createElementNS(ns, 'rect');
    rect.setAttribute('x', r.x); rect.setAttribute('y', r.y);
    rect.setAttribute('width', Math.max(0, r.w)); rect.setAttribute('height', Math.max(0, r.h));
    rect.setAttribute('rx', 2); rect.setAttribute('class', 'tile');
    rect.setAttribute('fill', ramps[Math.min(n - 1, i)]);
    const t = document.createElementNS(ns, 'title');
    if (r._group) {
      t.textContent = `${othersLabel} · ${r._count} 项（点击展开）`;
    } else {
      const real = sorted.find(s => s.name === r.name);
      t.textContent = `${r.name} · ${fmt(real ? real.value : r.value)} ${unit}${onSelect && r.path ? '（点击下钻）' : ''}`;
    }
    rect.appendChild(t);
    g.appendChild(rect);
    // 面积足够才放标签（§7 截断规则: 放不下不放，不挤）；数值显示真实大小而非缩放值
    const real = r._group ? null : sorted.find(s => s.name === r.name);
    const realValue = real ? real.value : r.value;
    if (r._group) {
      const name = document.createElementNS(ns, 'text');
      name.setAttribute('x', r.x + 10); name.setAttribute('y', r.y + 20);
      name.setAttribute('class', 'tname');
      name.textContent = `${othersLabel} · ${r._count} 项`;
      g.appendChild(name);
      const size = document.createElementNS(ns, 'text');
      size.setAttribute('x', r.x + 10); size.setAttribute('y', r.y + 36);
      size.setAttribute('class', 'tsize');
      size.textContent = '点击展开';
      g.appendChild(size);
    } else if (r.w > 64 && r.h > 40) {
      const name = document.createElementNS(ns, 'text');
      name.setAttribute('x', r.x + 10); name.setAttribute('y', r.y + 20);
      name.setAttribute('class', 'tname');
      const maxCh = Math.floor((r.w - 20) / 12);
      name.textContent = r.name.length > maxCh ? r.name.slice(0, maxCh - 1) + '…' : r.name;
      g.appendChild(name);
      const size = document.createElementNS(ns, 'text');
      size.setAttribute('x', r.x + 10); size.setAttribute('y', r.y + 36);
      size.setAttribute('class', 'tsize');
      size.textContent = `${fmt(realValue)} ${unit}`.trimEnd();
      g.appendChild(size);
    } else if (r.w > 30 && r.h > 20) {
      const size = document.createElementNS(ns, 'text');
      size.setAttribute('x', r.x + 6); size.setAttribute('y', r.y + 14);
      size.setAttribute('class', 'tsize');
      size.textContent = fmt(realValue);
      g.appendChild(size);
    }
    svg.appendChild(g);
  }
  el.innerHTML = '';
  el.appendChild(svg);
  return { others };
}
