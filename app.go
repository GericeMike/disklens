package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"disklens/internal/ai"
	"disklens/internal/history"
	"disklens/internal/kb"
	"disklens/internal/recycle"
	"disklens/internal/scanner"

	"golang.org/x/sys/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App Wails 绑定层：扫描 / 树查询 / 知识库解释 / 历史对比
type App struct {
	ctx        context.Context
	mu         sync.Mutex
	last       *scanner.Result
	index      kb.TreeIndex
	scanning   atomic.Bool
	catWalking atomic.Bool // 类型明细实时遍历进行中（防重入）
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// ---------- 数据传输结构 ----------

type DriveOption struct {
	Letter  string `json:"letter"`
	Total   uint64 `json:"total"`
	Free    uint64 `json:"free"`
	Current bool   `json:"current"`
}

type DriveStat struct {
	Letter string  `json:"letter"`
	Total  uint64  `json:"total"`
	Free   uint64  `json:"free"`
	UsedPc float64 `json:"usedPct"`
	AtRisk bool    `json:"atRisk"`
}

type FileRow struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Risk    string `json:"risk,omitempty"`  // 知识库风险等级（无命中留空）
	KBName  string `json:"kbName,omitempty"` // 知识库条目名（无命中留空）
}

type TypeRow struct {
	Category string `json:"category"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
}

type TreeNode struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Files   int64  `json:"files"`
	IsDir   bool   `json:"isDir"`
	Risk    string `json:"risk,omitempty"`
	KBName  string `json:"kbName,omitempty"`
}

type KBHitOut struct {
	EntryID   string             `json:"entryId"`
	Name      string             `json:"name"`
	Cat       string             `json:"cat"`
	Risk      string             `json:"risk"`
	Desc      string             `json:"desc"`
	Why       string             `json:"why"`
	Exists    []string           `json:"exists"`
	Sizes     map[string]int64   `json:"sizes"`
	Clean     []kb.CleanItem     `json:"clean,omitempty"`
	Related   []kb.RelatedPath   `json:"related,omitempty"`
}

type HistoryItem struct {
	Index     int      `json:"index"`
	Time      string   `json:"time"`
	Scope     []string `json:"scope"`
	Total     int64    `json:"total"`
	Duration  float64  `json:"duration"`
}

type OverviewData struct {
	Ready      bool            `json:"ready"`
	Duration   float64         `json:"durationSec"`
	Drives     []DriveStat     `json:"drives"`
	Roots      []string        `json:"roots"` // 扫描范围根（子文件夹扫描时 treemap 默认入口）
	TotalBytes int64           `json:"totalBytes"`
	TotalFiles int64           `json:"totalFiles"`
	TotalDirs  int64           `json:"totalDirs"`
	Errors     int64           `json:"errors"`
	FinishedAt string          `json:"finishedAt"`
	Finding    string          `json:"finding"`
	TopFiles   []FileRow       `json:"topFiles"`
	TypeStats  []TypeRow       `json:"typeStats"`
	ScanTime   string          `json:"scanTime"`
}

// ---------- 绑定方法 ----------

// GetDrives 枚举固定磁盘供扫描范围选择
func (a *App) GetDrives() []DriveOption {
	out := []DriveOption{}
	cwd, _ := filepath.Abs(".")
	cwdVol := strings.ToLower(filepath.VolumeName(cwd))
	for _, letter := range scanner.ListFixedDrives() {
		var free, total uint64
		if err := freeSpace(letter, &free, &total); err != nil {
			continue
		}
		out = append(out, DriveOption{
			Letter: letter, Total: total, Free: free,
			Current: strings.ToLower(filepath.VolumeName(letter)) == cwdVol,
		})
	}
	return out
}

// SelectDirectory 弹出系统目录选择对话框（用户主动触发；仅返回路径字符串）
func (a *App) SelectDirectory() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("对话框仅可在应用内使用")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择要扫描的文件夹",
	})
}

// emit 安全事件推送：ctx 未就绪（如单元测试）时静默跳过
func (a *App) emit(event string, args ...any) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, event, args...)
}

// StartScan 启动后台扫描（异步，默认增量模式：无匹配历史时自动退化为全量），
// 进度经事件推送：scan:progress / scan:done / scan:error
func (a *App) StartScan(roots []string) error {
	return a.StartScanEx(roots, true)
}

// StartScanEx 指定是否增量扫描。增量模式复用未变化的叶子目录（目录 mtime 判定），
// 结构变化（增/删/改名）任意深度都能检出；叶子目录内文件原地改写大小属盲区，
// 需要绝对精确时前端应传 incremental=false。
func (a *App) StartScanEx(roots []string, incremental bool) error {
	if !a.scanning.CompareAndSwap(false, true) {
		return fmt.Errorf("已有扫描正在进行")
	}
	if len(roots) == 0 {
		roots = scanner.ListFixedDrives()
	}
	go func() {
		defer a.scanning.Store(false)
		opt := scanner.Options{
			Roots:   roots,
			Workers: 8,
			Progress: func(files, dirs int64, bytes int64) {
				a.emit("scan:progress", map[string]any{
					"files": files, "dirs": dirs, "bytes": bytes,
				})
			},
		}
		if incremental {
			opt.Incremental = true
			opt.Prev = history.PrevForScope(roots) // nil=无匹配历史，Scan 自动全量
		}
		res, err := scanner.Scan(opt)
		if err != nil {
			a.emit("scan:error", err.Error())
			return
		}
		a.mu.Lock()
		a.last = res
		a.index = kb.BuildTreeIndex(res)
		a.mu.Unlock()
		if a.ctx != nil { // 测试模式（无生命周期 ctx）不落存档，避免污染历史
			if _, err := history.Save(res); err != nil {
				a.emit("scan:error", "存档失败: "+err.Error())
			}
		}
		a.emit("scan:done", map[string]any{
			"durationSec": res.Duration,
			"errors":      res.Errors,
			"finishedAt":  res.FinishedAt.Format("01-02 15:04"),
		})
	}()
	return nil
}

// IsScanning 是否正在扫描
func (a *App) IsScanning() bool { return a.scanning.Load() }

// LoadLatestScan 启动时自动加载最近一次存档，恢复概览/树/清理清单
func (a *App) LoadLatestScan() bool {
	snap, err := history.LoadLatest()
	if err != nil {
		return false
	}
	res := history.SnapshotToResult(snap)
	a.mu.Lock()
	a.last = res
	a.index = kb.BuildTreeIndex(res)
	a.mu.Unlock()
	return true
}

// GetOverview 概览页数据
func (a *App) GetOverview() *OverviewData {
	a.mu.Lock()
	res := a.last
	a.mu.Unlock()
	if res == nil {
		return &OverviewData{Ready: false}
	}
	d := &OverviewData{
		Ready:      true,
		Duration:   res.Duration,
		Roots:      []string{},
		TotalBytes: res.TotalBytes,
		TotalFiles: res.TotalFiles,
		TotalDirs:  res.TotalDirs,
		Errors:     res.Errors,
		FinishedAt: res.FinishedAt.Format("01-02 15:04"),
		ScanTime:   res.FinishedAt.Format("01-02 15:04"),
	}
	for _, r := range res.Roots {
		d.Roots = append(d.Roots, r.Name)
	}
	for _, dv := range res.Drives {
		usedPc := 0.0
		if dv.TotalBytes > 0 {
			usedPc = float64(dv.TotalBytes-dv.FreeBytes) / float64(dv.TotalBytes) * 100
		}
		d.Drives = append(d.Drives, DriveStat{
			Letter: dv.Letter, Total: dv.TotalBytes, Free: dv.FreeBytes,
			UsedPc: usedPc, AtRisk: dv.TotalBytes > 0 && float64(dv.FreeBytes)/float64(dv.TotalBytes) < 0.15,
		})
	}
	// 最大文件榜前 15（带知识库标签）
	for i, f := range res.TopFiles {
		if i == 15 {
			break
		}
		row := FileRow{Path: f.Path, Size: f.Size, ModTime: f.ModTime.Format("2006-01-02")}
		tagFileRow(&row)
		d.TopFiles = append(d.TopFiles, row)
	}
	// 类型统计前 10
	for i, t := range res.TypeStats {
		if i == 10 || t.Files == 0 {
			break
		}
		d.TypeStats = append(d.TypeStats, TypeRow{Category: t.Category, Bytes: t.Bytes, Files: t.Files})
	}
	// 一句话发现：扫描范围里最大的直接子目录
	bestName, bestBytes := "", int64(0)
	for _, r := range res.Roots {
		sep := ""
		if !strings.HasSuffix(r.Name, `\`) {
			sep = `\`
		}
		for _, c := range r.Children {
			if c.Bytes > bestBytes {
				bestBytes, bestName = c.Bytes, r.Name+sep+c.Name
			}
		}
	}
	if bestName != "" {
		d.Finding = fmt.Sprintf("最大目录：%s（%s）", bestName, humanBytes(bestBytes))
	}
	return d
}

// tagFileRow 给文件行补知识库标签（风险等级 + 条目名；无命中留空）
func tagFileRow(f *FileRow) {
	if hits := kb.Match(f.Path); len(hits) > 0 {
		f.Risk = hits[0].Entry.Risk
		f.KBName = hits[0].Entry.Name
	}
}

// GetCategoryFiles 某文件类型大类的文件明细：实时遍历本存档扫描根，
// 按大小降序返回前 200 条（树上不存文件明细，按需读盘；只读本地，不联网）。
func (a *App) GetCategoryFiles(category string) ([]FileRow, error) {
	if strings.TrimSpace(category) == "" {
		return nil, fmt.Errorf("未指定文件类型")
	}
	if !a.catWalking.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有类型统计在进行中，请稍候")
	}
	defer a.catWalking.Store(false)
	a.mu.Lock()
	res := a.last
	a.mu.Unlock()
	if res == nil {
		return nil, fmt.Errorf("没有扫描数据，请先扫描")
	}
	roots := make([]string, 0, len(res.Roots))
	for _, r := range res.Roots {
		roots = append(roots, r.Name)
	}
	infos := scanner.CollectCategoryFiles(roots, category, 200)
	out := make([]FileRow, 0, len(infos))
	for _, f := range infos {
		row := FileRow{Path: f.Path, Size: f.Size, ModTime: f.ModTime.Format("2006-01-02")}
		tagFileRow(&row)
		out = append(out, row)
	}
	return out, nil
}

// GetChildren 某路径的子目录（treemap 下钻）
func (a *App) GetChildren(path string) []TreeNode {
	a.mu.Lock()
	idx := a.index
	a.mu.Unlock()
	if idx == nil {
		return nil
	}
	kids := kb.ChildrenOf(idx, path)
	out := make([]TreeNode, 0, len(kids))
	for _, k := range kids {
		t := TreeNode{Path: k.Path, Name: nodeName(k.Path), Bytes: k.Size, Files: k.Files, IsDir: true}
		if hits := kb.Match(k.Path); len(hits) > 0 {
			t.Risk = hits[0].Entry.Risk
			t.KBName = hits[0].Entry.Name
		}
		out = append(out, t)
	}
	return out
}

// GetRoots 扫描范围根（treemap 顶层入口）
func (a *App) GetRoots() []TreeNode {
	a.mu.Lock()
	res := a.last
	a.mu.Unlock()
	if res == nil {
		return nil
	}
	out := []TreeNode{}
	for _, r := range res.Roots {
		t := TreeNode{Path: r.Name, Name: r.Name, Bytes: r.Bytes, Files: r.Files, IsDir: true}
		if hits := kb.Match(r.Name); len(hits) > 0 {
			t.Risk = hits[0].Entry.Risk
			t.KBName = hits[0].Entry.Name
		}
		out = append(out, t)
	}
	return out
}

// ExplainPath 知识库解释 + 各落盘点在本机扫描中的大小
func (a *App) ExplainPath(path string) []KBHitOut {
	hits := kb.Match(path)
	out := []KBHitOut{}
	a.mu.Lock()
	idx := a.index
	a.mu.Unlock()
	for _, h := range hits {
		o := KBHitOut{
			EntryID: h.Entry.ID, Name: h.Entry.Name, Cat: h.Entry.Cat, Risk: h.Entry.Risk,
			Desc: h.Entry.Desc, Why: h.Entry.Why, Exists: h.Exists,
			Clean: h.Entry.Clean, Related: h.Entry.Related,
			Sizes: map[string]int64{},
		}
			if idx != nil {
				for _, p := range h.Exists {
					if n, ok := idx[strings.ToLower(filepath.Clean(p))]; ok {
						o.Sizes[p] = n.Bytes
					}
				}
				if n, ok := idx[strings.ToLower(filepath.Clean(path))]; ok {
					o.Sizes["__self"] = n.Bytes
				} else if fi, err := os.Stat(path); err == nil {
					// 文件不在目录树（树只存目录），按实际大小给 __self
					o.Sizes["__self"] = fi.Size()
				}
			}
		out = append(out, o)
	}
	return out
}

// ---------- AI 解释（仅用户主动触发时联网；Key 经 DPAPI 加密存储） ----------

// AIProviderOut 预设信息（前端渲染选择卡片）
type AIProviderOut struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"defaultModel"`
	KeyHint      string `json:"keyHint"`
	NeedBaseURL  bool   `json:"needBaseUrl"` // custom 需要用户填地址
}

// AIConfigOut 脱敏后的配置（Key 只回前 4 位）
type AIConfigOut struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	BaseURL  string          `json:"baseUrl"`
	KeyMask  string          `json:"keyMask"` // 空=未配置
	HasKey   bool            `json:"hasKey"`
	Presets  []AIProviderOut `json:"presets"`
}

// GetAIConfig 读取 AI 配置（Key 脱敏展示）
func (a *App) GetAIConfig() AIConfigOut {
	c := ai.LoadConfig()
	out := AIConfigOut{
		Provider: c.Provider, Model: c.Model, BaseURL: c.BaseURL,
		HasKey: c.ApiKey != "", Presets: []AIProviderOut{},
	}
	if out.HasKey {
		out.KeyMask = ai.MaskKey(ai.PlainKey())
	}
	for id, p := range ai.Presets {
		out.Presets = append(out.Presets, AIProviderOut{
			ID: id, Name: p.Name, DefaultModel: p.DefaultModel, KeyHint: p.KeyHint,
			NeedBaseURL: id == "custom",
		})
	}
	return out
}

// SaveAIConfig 保存配置；apiKey 语义：空串=保留旧值，"-"=清除
func (a *App) SaveAIConfig(provider, apiKey, model, baseURL string) (AIConfigOut, error) {
	if _, err := ai.SaveConfig(provider, model, baseURL, apiKey); err != nil {
		return AIConfigOut{}, err
	}
	return a.GetAIConfig(), nil
}

// TestAI 测试连接：发一条最小请求验证 Key 与网络（用户点「测试连接」才联网）
func (a *App) TestAI() (string, error) {
	cfg := ai.LoadConfig()
	apiKey := ai.PlainKey()
	if apiKey == "" {
		return "", fmt.Errorf("尚未配置 API Key")
	}
	return ai.Chat(cfg, apiKey, "你是连通性测试。", "请只回复四个字：连接成功", nil)
}

// AIExplainOut AI 解释结果
type AIExplainOut struct {
	Text       string `json:"text"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"` // false=未配置 Key
}

// ExplainWithAI 对某目录做 AI 解读：扫描树上下文 + 实时文件样本 + 知识库命中，
// 构造提示词调用所选模型。仅在前端点击「AI 解释」按钮时被调用。
func (a *App) ExplainWithAI(path string) (AIExplainOut, error) {
	out := AIExplainOut{}
	if path == "" {
		return out, fmt.Errorf("路径为空")
	}
	cfg := ai.LoadConfig()
	apiKey := ai.PlainKey()
	if apiKey == "" {
		return out, fmt.Errorf("尚未配置 AI：请到「设置」页选择提供商并填写 API Key")
	}
	// 目标可能是目录或单个文件：文件不在树里，按实际大小与文件名构造上下文
	fi, statErr := os.Stat(path)
	isFile := statErr == nil && !fi.IsDir()
	var size, files int64
	sampleDirs := []string{}
	if isFile {
		size, files = fi.Size(), 1
	} else {
		idxKey := strings.ToLower(filepath.Clean(path))
		a.mu.Lock()
		idx := a.index
		a.mu.Unlock()
		if idx != nil {
			if n, ok := idx[idxKey]; ok {
				size, files = n.Bytes, n.Files
				for i, c := range n.Children {
					if i >= 8 {
						break
					}
					sampleDirs = append(sampleDirs, fmt.Sprintf("%s(%s)", c.Name, humanBytes(c.Bytes)))
				}
			}
		}
	}
	// 实时文件样本（树里没有文件节点；文件目标即样本本身）
	sampleFiles := []string{}
	if isFile {
		sampleFiles = append(sampleFiles, filepath.Base(path))
	} else if ents, err := os.ReadDir(path); err == nil {
		n := 0
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			if n >= 10 {
				break
			}
			sampleFiles = append(sampleFiles, e.Name())
			n++
		}
	}
	// 知识库命中作为参考
	kbHits := []string{}
	for _, h := range kb.Match(path) {
		kbHits = append(kbHits, fmt.Sprintf("%s（%s，风险:%s）：%s",
			h.Entry.Name, h.Entry.Cat, kb.RiskMark(h.Entry.Risk), h.Entry.Desc))
	}
	system, user := ai.ExplainPrompt(path, size, files, kbHits, sampleDirs, sampleFiles)
	text, err := ai.Chat(cfg, apiKey, system, user, nil)
	if err != nil {
		out.Model, out.Configured = cfg.Model, true
		return out, err
	}
	return AIExplainOut{Text: text, Model: cfg.Model, Configured: true}, nil
}

// GetCleanSummary 知识库驱动的可清理汇总；drive 如 "C:\"，空串=全部盘
func (a *App) GetCleanSummary(drive string) []kb.CleanSummaryEntry {
	a.mu.Lock()
	res := a.last
	a.mu.Unlock()
	return kb.BuildCleanSummary(res, 200<<20, 20, drive) // ≥200MB 才进清单
}

// OpenFolder 在资源管理器中打开文件夹（用户主动触发；仅本机窗口，不联网）
func (a *App) OpenFolder(path string) error {
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("路径不存在: %s", path)
	}
	// explorer 打开目录；explorer 的返回码不可靠，只 Start 不 Wait
	return exec.Command("explorer.exe", path).Start()
}

// OpenFileLocation 在资源管理器中打开文件所在文件夹并选中该文件（用户主动触发）
func (a *App) OpenFileLocation(path string) error {
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("路径不存在: %s", path)
	}
	// explorer /select, 会在父窗口中高亮该文件；返回码不可靠，只 Start 不 Wait
	return exec.Command("explorer.exe", "/select,"+path).Start()
}

// DeleteResult 单项删除结果
type DeleteResult struct {
	Path   string `json:"path"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// DeleteToRecycle 送回收站删除（系统路径硬锁在 recycle 包内强制校验）
func (a *App) DeleteToRecycle(paths []string) []DeleteResult {
	if len(paths) == 0 {
		return []DeleteResult{}
	}
	// 删除前记录文件大小：树里没有文件节点，删除后按大小从父链扣减
	fileSizes := map[string]int64{}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			fileSizes[strings.ToLower(filepath.Clean(p))] = fi.Size()
		}
	}
	res := recycle.SendToRecycle(paths)
	out := make([]DeleteResult, 0, len(res))
	var removed []string
	var removedFiles []string
	for _, r := range res {
		out = append(out, DeleteResult{Path: r.Path, OK: r.OK, Reason: r.Reason})
		if !r.OK {
			continue
		}
		key := strings.ToLower(filepath.Clean(r.Path))
		a.mu.Lock()
		_, inTree := a.index[key]
		a.mu.Unlock()
		if inTree {
			removed = append(removed, r.Path) // 目录：走整树摘除
		} else if sz, ok := fileSizes[key]; ok {
			removedFiles = append(removedFiles, r.Path+"\x00"+fmt.Sprint(sz))
		}
	}
	// 内存扫描树同步移除（父链减值 + 索引清理），GUI 刷新后不再显示已删目录
	a.removeNodes(removed)
	for _, rf := range removedFiles {
		i := strings.IndexByte(rf, '\x00')
		sz, _ := strconv.ParseInt(rf[i+1:], 10, 64)
		a.subtractFromParent(rf[:i], sz, 1)
	}
	a.emit("deleted:done", map[string]any{"count": len(out)})
	return out
}

// subtractFromParent 文件从树中扣除：父链逐级减 Bytes/Files（文件不在 Children，无节点可摘）
func (a *App) subtractFromParent(p string, bytes, files int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.index == nil {
		return
	}
	cur := filepath.Dir(strings.ToLower(filepath.Clean(p)))
	for {
		node, ok := a.index[strings.ToLower(cur)]
		if !ok {
			return // 超出存档根（裁剪深度外），尽力而为
		}
		node.Bytes -= bytes
		node.Files -= files
		parent := filepath.Dir(cur)
		if strings.EqualFold(parent, cur) {
			return
		}
		cur = parent
	}
}

// ListFiles 某目录下的文件列表（叶子目录内容查看；按大小降序，最多 200 条）
func (a *App) ListFiles(path string) []FileRow {
	out := []FileRow{}
	ents, err := os.ReadDir(path)
	if err != nil {
		return out
	}
	type fileItem struct {
		name string
		size int64
		mt   time.Time
	}
	var files []fileItem
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		files = append(files, fileItem{e.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })
	for i, f := range files {
		if i >= 200 {
			break
		}
		row := FileRow{
			Path:    filepath.Join(path, f.name),
			Size:    f.size,
			ModTime: f.mt.Format("2006-01-02 15:04"),
		}
		tagFileRow(&row)
		out = append(out, row)
	}
	return out
}

// removeNodes 从内存树/索引中移除已删除目录，祖先逐级扣减聚合值
func (a *App) removeNodes(paths []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := a.index
	if idx == nil {
		return
	}
	for _, p := range paths {
		key := strings.ToLower(filepath.Clean(p))
		node, ok := idx[key]
		if !ok {
			continue
		}
		// 从父节点 Children 中摘除
		parentKey := strings.ToLower(filepath.Dir(key))
		if parent, ok := idx[parentKey]; ok {
			for i, c := range parent.Children {
				if c == node {
					parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
					break
				}
			}
		}
		// 祖先链逐级减值（含根）
		curPath := key
		for {
			cur, ok := idx[curPath]
			if !ok {
				break
			}
			cur.Bytes -= node.Bytes
			cur.Files -= node.Files
			cur.Dirs -= node.Dirs + 1
			parent := filepath.Dir(curPath)
			if strings.EqualFold(parent, curPath) {
				break // 已到盘根
			}
			curPath = parent
		}
		// 索引中清除该子树所有键
		prefix := key + `\`
		for k := range idx {
			if k == key || strings.HasPrefix(k, prefix) {
				delete(idx, k)
			}
		}
	}
}

// CheckDelete 预检：返回每个路径的锁定原因（空串=允许），供确认弹窗展示
func (a *App) CheckDelete(paths []string) []DeleteResult {
	out := make([]DeleteResult, 0, len(paths))
	for _, p := range paths {
		out = append(out, DeleteResult{Path: p, OK: recycle.Check(p) == "", Reason: recycle.Check(p)})
	}
	return out
}

// ListHistoryScans 历史列表（新→旧）
func (a *App) ListHistoryScans() []HistoryItem {
	names, err := history.List()
	if err != nil {
		return nil
	}
	out := []HistoryItem{}
	for i, name := range names {
		snap, err := history.LoadByName(name)
		if err != nil {
			continue
		}
		out = append(out, HistoryItem{
			Index: i + 1, Time: snap.Meta.FinishedAt.Format("2006-01-02 15:04"),
			Scope: snap.Meta.Scope, Total: snap.Meta.Totals.Bytes, Duration: snap.Meta.DurationSec,
		})
	}
	return out
}

// CompareHistory 对比两份历史（1-based，a 为较旧的一份）
func (a *App) CompareHistory(aIdx, bIdx int, minGB float64) ([]history.Delta, error) {
	names, err := history.List()
	if err != nil {
		return nil, err
	}
	if aIdx < 1 || bIdx < 1 || aIdx > len(names) || bIdx > len(names) || aIdx == bIdx {
		return nil, fmt.Errorf("历史序号无效")
	}
	sa, err := history.LoadByName(names[aIdx-1])
	if err != nil {
		return nil, err
	}
	sb, err := history.LoadByName(names[bIdx-1])
	if err != nil {
		return nil, err
	}
	return history.Compare(sa, sb, int64(minGB*1024*1024*1024))
}

// ---------- helpers ----------

func freeSpace(root string, free, total *uint64) error {
	return windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(root), nil, total, free)
}

func nodeName(p string) string {
	p = strings.TrimRight(p, `\`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func humanBytes(b int64) string {
	const unit = 1024
	abs := b
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= unit*unit*unit*unit:
		return fmt.Sprintf("%.2f TB", float64(b)/(unit*unit*unit*unit))
	case abs >= unit*unit*unit:
		return fmt.Sprintf("%.1f GB", float64(b)/(unit*unit*unit))
	case abs >= unit*unit:
		return fmt.Sprintf("%.1f MB", float64(b)/(unit*unit))
	case abs >= unit:
		return fmt.Sprintf("%.1f KB", float64(b)/unit)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

var _ = time.Now
