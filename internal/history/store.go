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
const pruneDepth = 4

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
		v := filepath.VolumeName(r.Path0)
		if v == "" {
			v = strings.TrimSuffix(r.Name, `\`)
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

// clonePruned 深拷贝并按层裁剪
func clonePruned(nodes []*scanner.Node, depth int) []*scanner.Node {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]*scanner.Node, 0, len(nodes))
	for _, n := range nodes {
		c := &scanner.Node{Name: n.Name, Bytes: n.Bytes, Files: n.Files, Dirs: n.Dirs}
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
