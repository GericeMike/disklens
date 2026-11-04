package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"disklens/internal/history"
	"disklens/internal/kb"
	"disklens/internal/scanner"

	"golang.org/x/sys/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App Wails 绑定层：扫描 / 树查询 / 知识库解释 / 历史对比
type App struct {
	ctx      context.Context
	mu       sync.Mutex
	last     *scanner.Result
	index    kb.TreeIndex
	scanning atomic.Bool
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
	Path    string  `json:"path"`
	Size    int64   `json:"size"`
	ModTime string  `json:"modTime"`
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

// emit 安全事件推送：ctx 未就绪（如单元测试）时静默跳过
func (a *App) emit(event string, args ...any) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, event, args...)
}

// StartScan 启动后台扫描（异步），进度经事件推送：scan:progress / scan:done / scan:error
func (a *App) StartScan(roots []string) error {
	if !a.scanning.CompareAndSwap(false, true) {
		return fmt.Errorf("已有扫描正在进行")
	}
	if len(roots) == 0 {
		roots = scanner.ListFixedDrives()
	}
	go func() {
		defer a.scanning.Store(false)
		res, err := scanner.Scan(scanner.Options{
			Roots:   roots,
			Workers: 8,
			Progress: func(files, dirs int64, bytes int64) {
				a.emit("scan:progress", map[string]any{
					"files": files, "dirs": dirs, "bytes": bytes,
				})
			},
		})
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
		TotalBytes: res.TotalBytes,
		TotalFiles: res.TotalFiles,
		TotalDirs:  res.TotalDirs,
		Errors:     res.Errors,
		FinishedAt: res.FinishedAt.Format("01-02 15:04"),
		ScanTime:   res.FinishedAt.Format("01-02 15:04"),
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
	// 最大文件榜前 15
	for i, f := range res.TopFiles {
		if i == 15 {
			break
		}
		d.TopFiles = append(d.TopFiles, FileRow{Path: f.Path, Size: f.Size, ModTime: f.ModTime.Format("2006-01-02")})
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
				if n, ok := idx[strings.ToLower(p)]; ok {
					o.Sizes[p] = n.Bytes
				}
			}
			if n, ok := idx[strings.ToLower(path)]; ok {
				o.Sizes["__self"] = n.Bytes
			}
		}
		out = append(out, o)
	}
	return out
}

// GetCleanSummary 知识库驱动的可清理汇总
func (a *App) GetCleanSummary() []kb.CleanSummaryEntry {
	a.mu.Lock()
	res := a.last
	a.mu.Unlock()
	return kb.BuildCleanSummary(res, 200<<20, 20) // ≥200MB 才进清单
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
