# 盘镜 DiskLens

Windows 本地磁盘空间分析器：扫一遍盘，看清空间被谁吃了，找出能安全清理的部分。

- **快**：并发扫描引擎，C+D 双盘 270 万文件实测约 15 秒
- **看得清**：treemap 方块地图按占比铺开整个盘，点方块即下钻，目录列表与文件清单联动
- **删得放心**：删除一律送回收站；Windows / Program Files / 盘根等系统路径硬锁，锁了就点不动
- **零联网**：不联网也能完整使用；唯一的联网功能是 AI 解释，且需要你主动配置 Key 并点击才发起请求
- **纯本地**：无遥测、无后台进程、字体自托管；AI Key 用 Windows DPAPI 加密存储，不明文落盘

## 功能特性

### 扫描与分析
- 多路径扫描：默认扫全部固定磁盘，也可扫任意子文件夹（如只扫 `D:\games`）
- treemap 方块地图：目录按空间占比铺成方块，支持逐层下钻、小项聚合展开（「其他·N 项」）
- 目录列表联动：方块地图与目录列表双向联动，可清理项独立标注
- 叶子目录文件清单：进入没有子目录的文件夹时直接列出其中的文件（前 200 条），可逐文件删除
- 扫描历史：每次扫描自动存档，下次启动自动恢复上次结果；支持双盘混合扫描
- 对比分析：对比最近两次扫描，变化按目录树折叠汇总，一眼看出「谁长大了」

### 清理辅助
- 可清理项知识库：内置 151 条常见目录/文件说明（缓存、日志、旧版本残留……），随盘符过滤
- 命令行解释：`explain <路径>` 直接告诉您这个文件夹是干什么的、能不能删
- 解释抽屉：GUI 内选中任意目录即可查看解释，并可一键打开该文件夹
- AI 补充解读（可选）：支持智谱 glm-4-flash / DeepSeek / Kimi / 自定义 OpenAI 兼容接口；
  面向小白的「说人话」提示词，结合本机实际文件样本生成，点击按钮才联网

### 删除
- 删除一律进回收站（`SHFileOperation` + `FOF_ALLOWUNDO`），可随时还原
- 系统路径硬锁：盘根、Windows、Program Files（含 x86）、ProgramData、用户目录外的系统目录等
  一律拒绝删除，且不泄露路径存在性
- 删除前强制确认：弹窗先做锁定预检（「检查中…」→「确认删除」/红字拒绝），回车默认落在「取消」

### 界面
- 双主题：终端深色 / 白纸浅色，在设置中切换，默认深色
- 自研 treemap 渲染，无前端框架依赖，字体全部本地加载

## 使用说明

### 图形界面

直接运行 `disklens.exe`：

1. 首次进入点击「扫描」，选择全部磁盘或添加自定义子文件夹路径
2. 扫描完成后在概览页查看各盘占用，点击盘标签切换
3. 「方块地图」页点方块下钻、面包屑返回；顶层被裁剪到扫描根，不会出现点了是空的盘符
4. 目录列表中：🗑 删除文件夹/文件（送回收站，先预检确认）；点「其他·N 项」展开小目录
5. 选中目录后右侧可打开解释抽屉；「AI 补充解读」需先在设置页配置 AI Key
6. 设置页可切换主题、配置 AI（四预设 + 自定义接口）

### 命令行

```text
disklens                     打开图形界面
disklens scan [路径...]      扫描（默认: 全部固定磁盘），结果存入历史
disklens compare             对比最近两次扫描，看谁长大了
disklens list                列出历史扫描
disklens explain <路径>      解释这个文件夹是干什么的、能不能删
```

示例：

```bash
disklens scan C:\ D:\          # 扫 C 盘和 D 盘
disklens scan -workers 16      # 加大并发
disklens compare --min-gb 1    # 只看变化超过 1GB 的目录
disklens explain "C:\Users\me\AppData\Local\uv\cache"
```

## 构建方法

前置要求（Windows）：

- Go ≥ 1.26
- Node.js（前端构建用）
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`

```bash
wails build        # 产物: build/bin/disklens.exe（单文件，可直接分发）
wails dev          # 开发模式（Vite 热重载 + Go 绑定调试）
go test ./...      # 单元测试（扫描器级联/对比折叠/删除硬锁/AI 配置等）
```

应用图标在 `build/appicon.png`（1024×1024），`wails build` 自动生成多尺寸 ico。

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.26 + [Wails v2](https://wails.io) |
| 前端 | Vanilla JS + Vite，自研 treemap 布局，无框架 |
| 系统调用 | `golang.org/x/sys`（回收站删除等 Win32 调用） |
| AI | OpenAI 兼容 `/chat/completions`，DPAPI 加密存 Key |
| 字体 | IBM Plex / JetBrains Mono / Public Sans / Nunito（自托管） |

界面双风格源自 M0 阶段的设计推演，风格定义存档于 `.taste-skill/direction.json`。

## 隐私与安全

- 不采集任何数据，无遥测、无自动更新检查
- 除用户主动配置并点击「AI 补充解读」外，程序不发起任何网络请求
- AI Key 经 Windows DPAPI 加密后存储，绑定当前用户，不明文落盘
- 删除操作全部走系统回收站，系统目录在代码层硬锁
