// 盘镜 DiskLens — 预览样例数据（ believable、可对账；正式版由扫描引擎填充 ）
// 磁盘实况演示数据（2026-11-04 Get-CimInstance），目录数字为演示值，交付前由真实扫描替换。

export const drives = {
  C: { label: 'C 盘 · NVMe 系统盘', total: 512, free: 256 },
  D: { label: 'D 盘 · 数据盘', total: 1024, free: 100, atRisk: true },
};

// D 盘顶层目录（GB），sum === used
export const dTree = [
  { name: '剪辑工程', value: 118.4 },
  { name: 'SteamLibrary', value: 96.3 },
  { name: '虚拟机', value: 49.2 },
  { name: '微信文件', value: 45.2 },
  { name: '备份', value: 41.8 },
  { name: '下载', value: 38.7 },
  { name: '网盘缓存', value: 22.4 },
  { name: '开发缓存', value: 12.8 },
  { name: '其他', value: 3.0 },
];

export const cleanItems = [
  { risk: 'safe',    name: 'Windows 临时文件',            where: 'C:\\Windows\\Temp',              size: 2.3,  reason: '系统临时目录，30 天未访问，可安全删除' },
  { risk: 'safe',    name: '浏览器缓存（Edge/Chrome）',    where: '各浏览器 Cache 目录',            size: 6.8,  reason: '缓存可随时重建，不影响账号与密码' },
  { risk: 'safe',    name: '开发缓存（npm/pip/Go）',       where: '各包管理器缓存目录',             size: 9.4,  reason: '依赖缓存，删除后下次构建自动重新下载' },
  { risk: 'caution', name: '下载目录旧安装包',            where: 'D:\\下载\\*.exe *.zip',          size: 11.2, reason: '安装完成的旧包，确认无需重装后可删' },
  { risk: 'caution', name: '微信图片视频缓存',            where: '微信缓存目录（不含聊天记录）',   size: 4.1,  reason: '删除后聊天图片需重新加载，记录不受影响' },
  { risk: 'system',  name: '休眠文件 hiberfil.sys',       where: 'C:\\hiberfil.sys',               size: 12.8, reason: '需在系统中关闭休眠后释放，工具不直接删除' },
];
// 直接可释放 = safe + caution
export const cleanDirect = 33.8;   // 2.3+6.8+9.4+11.2+4.1
export const cleanSystemOp = 12.8;

// 90 天增长 / 释放（D 盘主要变动项）
export const growth = {
  labels: ['微信文件', '剪辑工程', '下载', '备份', 'SteamLibrary'],
  up: [18.3, 9.6, 4.2, 2.1],
  down: [12.5],
};

export const topFiles = [
  { name: 'win11-dev.vhdx',            where: 'D:\\虚拟机',              size: 38.2, mtime: '2025-08-12' },
  { name: '客户A-成片.mp4',            where: 'D:\\剪辑工程\\客户A',     size: 24.6, mtime: '2026-09-30' },
  { name: 'blackmyth-wukong-4k.pak',   where: 'D:\\SteamLibrary\\steamapps', size: 21.9, mtime: '2026-10-18' },
  { name: '2026-10全盘备份.zip',       where: 'D:\\备份',                size: 18.4, mtime: '2026-10-26' },
  { name: 'ubuntu-24.04.1-desktop.iso', where: 'D:\\下载',               size: 5.8,  mtime: '2026-03-15' },
];

// ---- 对账断言（§2.1: 数字必须能合上） ----
const dUsed = +(drives.D.total - drives.D.free).toFixed(1);           // 427.8
const treeSum = +dTree.reduce((s, d) => s + d.value, 0).toFixed(1);
console.assert(Math.abs(treeSum - dUsed) < 0.01, `D盘 treemap 合计 ${treeSum} ≠ 已用 ${dUsed}`);
const cleanSum = +cleanItems.filter(i => i.risk !== 'system').reduce((s, i) => s + i.size, 0).toFixed(1);
console.assert(Math.abs(cleanSum - cleanDirect) < 0.01, `可清理合计 ${cleanSum} ≠ ${cleanDirect}`);
console.assert(Math.abs((drives.C.total - drives.C.free) - 256) < 0.01, 'C盘已用对不上');
