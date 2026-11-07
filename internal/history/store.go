// Package history — 扫描历史存档：JSON(裁剪树) + gzip，存于 %APPDATA%\DiskLens\scans
package history

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"disklens/internal/scanner"
)

// pruneDepth 历史树保留层数（根=0）。更深的层级丢弃子表，聚合值保留在祖先节点。
// 4 层时 AppData\Local（第 4 层）之下无法下钻；8 层覆盖 Users→AppData→Local→uv→cache 等常用深度。
const pruneDepth = 8

// Version 存档格式版本
const Version = 1

type Totals struct {
	Bytes int64 `json:"bytes"`
	Files int64 `json:"files"`
	Dirs  int64 `json:"dirs"`
}

type Meta struct {
	StartedAt   time.Time           `json:"startedAt"`
	FinishedAt  time.Time           `json:"finishedAt"`
	DurationSec float64             `json:"durationSec"`
	Scope       []string            `json:"scope"`
	Drives      []scanner.DriveInfo `json:"drives"`
	Totals      Totals              `json:"totals"`
	Errors      int64               `json:"errors"`
}

type Snapshot struct {
	Version   int                `json:"v"`
	Meta      Meta               `json:"meta"`
	Roots     []*scanner.Node    `json:"roots"`
	TopFiles  []scanner.FileInfo `json:"topFiles"`
	TypeStats []scanner.TypeStat `json:"typeStats"`
}

// Dir 存档目录
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "DiskLens", "scans")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// Save 保存扫描结果，返回存档路径
func Save(res *scanner.Result) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	tag := make([]string, 0, len(res.Roots))
	for _, r := range res.Roots {
		v := strings.TrimSuffix(filepath.VolumeName(r.Path0), ":") // 冒号在 Windows 文件名中非法（会被当作 NTFS 备用数据流）
		if v == "" {
			v = strings.TrimSuffix(r.Name, `:\`)
		}
		tag = append(tag, v)
	}
	name := res.StartedAt.Format("20060102-150405") + "_" + strings.Join(tag, "+") + ".json.gz"
	path := filepath.Join(d, name)

	snap := &Snapshot{
		Version:   Version,
		Meta:      toMeta(res),
		Roots:     clonePruned(res.Roots, 0),
		TopFiles:  res.TopFiles,
		TypeStats: res.TypeStats,
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	enc.SetIndent("", " ")
	if err := enc.Encode(snap); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func toMeta(res *scanner.Result) Meta {
	scope := make([]string, 0, len(res.Roots))
	for _, r := range res.Roots {
		scope = append(scope, r.Path0)
	}
	return Meta{
		StartedAt:   res.StartedAt,
		FinishedAt:  res.FinishedAt,
		DurationSec: res.Duration,
		Scope:       scope,
		Drives:      res.Drives,
		Totals:      Totals{Bytes: res.TotalBytes, Files: res.TotalFiles, Dirs: res.TotalDirs},
		Errors:      res.Errors,
	}
}

// clonePruned 深拷贝并按层裁剪（MTime/Cat/CatF 为增量扫描的嫁接依据，必须保留）
func clonePruned(nodes []*scanner.Node, depth int) []*scanner.Node {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]*scanner.Node, 0, len(nodes))
	for _, n := range nodes {
		c := &scanner.Node{Name: n.Name, Bytes: n.Bytes, Files: n.Files, Dirs: n.Dirs, MTime: n.MTime}
		if len(n.Cat) > 0 {
			c.Cat = append([]int64(nil), n.Cat...)
		}
		if len(n.CatF) > 0 {
			c.CatF = append([]int64(nil), n.CatF...)
		}
		if depth < pruneDepth {
			c.Children = clonePruned(n.Children, depth+1)
		}
		out = append(out, c)
	}
	return out
}

// List 列出全部存档，按时间倒序
func List() ([]string, error) {
	d, err := Dir()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// LoadByName 按文件名读取存档
func LoadByName(name string) (*Snapshot, error) {
	d, err := Dir()
	if err != nil {
		return nil, err
	}
	return loadFile(filepath.Join(d, name))
}

func loadFile(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("读取存档失败: %w", err)
	}
	var snap Snapshot
	if err := json.NewDecoder(gz).Decode(&snap); err != nil {
		return nil, fmt.Errorf("解析存档失败: %w", err)
	}
	return &snap, nil
}

// LoadLatest 读取最新一份存档
func LoadLatest() (*Snapshot, error) {
	names, err := List()
	if err != nil {
		return nil, fmt.Errorf("暂无历史存档")
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("暂无历史存档")
	}
	return LoadByName(names[0])
}

// PrevForScope 增量扫描取历史：按时间倒序找第一份扫描范围与 roots 完全一致的存档，
// 还原为可用扫描结果。找不到（无历史/范围不同/读取失败）返回 nil，调用方退化为全量。
func PrevForScope(roots []string) *scanner.Result {
	names, err := List()
	if err != nil {
		return nil
	}
	want := make(map[string]bool, len(roots))
	for _, r := range roots {
		want[strings.ToLower(scanner.NormalizeRoot(r))] = true
	}
	for _, name := range names {
		snap, err := LoadByName(name)
		if err != nil || len(snap.Meta.Scope) != len(roots) {
			continue
		}
		got := make(map[string]bool, len(snap.Meta.Scope))
		for _, s := range snap.Meta.Scope {
			got[strings.ToLower(s)] = true
		}
		same := len(got) == len(want)
		for k := range want {
			if !got[k] {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		return SnapshotToResult(snap)
	}
	return nil
}

// SnapshotToResult 把裁剪树存档还原为可用的扫描结果（重建各节点完整路径）
func SnapshotToResult(snap *Snapshot) *scanner.Result {
	res := &scanner.Result{
		StartedAt:  snap.Meta.StartedAt,
		FinishedAt: snap.Meta.FinishedAt,
		Duration:   snap.Meta.DurationSec,
		Drives:     snap.Meta.Drives,
		TopFiles:   snap.TopFiles,
		TypeStats:  snap.TypeStats,
		TotalBytes: snap.Meta.Totals.Bytes,
		TotalFiles: snap.Meta.Totals.Files,
		TotalDirs:  snap.Meta.Totals.Dirs,
		Errors:     snap.Meta.Errors,
	}
	var rebuild func(n *scanner.Node, prefix string)
	rebuild = func(n *scanner.Node, prefix string) {
		n.Path0 = prefix + "\\" + n.Name
		for _, c := range n.Children {
			rebuild(c, n.Path0)
		}
	}
	for _, r := range snap.Roots {
		// 保持存档中的原始根名（可能是盘根，也可能是子目录根），仅重建 Path0 链
		r.Path0 = r.Name
		// 根名以反斜杠结尾（C:\）时直接拼会得到 C:\\Users 双反斜杠，先去尾
		base := strings.TrimSuffix(r.Name, `\`)
		for _, c := range r.Children {
			rebuild(c, base)
		}
	}
	res.Roots = snap.Roots
	return res
}
