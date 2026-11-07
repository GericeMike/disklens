// Package scanner — 盘镜 DiskLens 扫描引擎数据模型
package scanner

import "time"

// Node 目录树节点，Bytes/Files/Dirs 含全部子孙聚合。
// Path0 为完整路径，仅扫描期使用，不序列化。
// MTime 为目录自身修改时间（UnixNano）：增量扫描据此判断「叶子目录未变」整树复用。
// Cat/CatF 为子树各文件类型大类的字节/文件数直方图（桶序见 CatOrder），
// 供增量嫁接时搬运类型统计；全零时省略。
type Node struct {
	Name     string  `json:"n"`
	Bytes    int64   `json:"b"`
	Files    int64   `json:"f"`
	Dirs     int64   `json:"d,omitempty"`
	Children []*Node `json:"c,omitempty"`

	MTime int64   `json:"mt,omitempty"`
	Cat   []int64 `json:"cb,omitempty"`
	CatF  []int64 `json:"cf,omitempty"`

	Path0 string `json:"-"`
}

// FileInfo 大文件记录
type FileInfo struct {
	Path    string    `json:"p"`
	Size    int64     `json:"s"`
	ModTime time.Time `json:"m"`
}

// DriveInfo 盘容量（扫描时点快照）
type DriveInfo struct {
	Letter     string `json:"letter"`
	TotalBytes uint64 `json:"total"`
	FreeBytes  uint64 `json:"free"`
}

// TypeStat 文件类型大类统计
type TypeStat struct {
	Category string `json:"cat"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
}

// Result 一次扫描的完整结果
type Result struct {
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt time.Time   `json:"finishedAt"`
	Duration   float64     `json:"durationSec"`
	Roots      []*Node     `json:"roots"`
	Drives     []DriveInfo `json:"drives"`
	TopFiles   []FileInfo  `json:"topFiles"`
	TypeStats  []TypeStat  `json:"typeStats"`
	TotalBytes int64       `json:"totalBytes"`
	TotalFiles int64       `json:"totalFiles"`
	TotalDirs  int64       `json:"totalDirs"`
	Errors     int64       `json:"errors"`
	ErrorSamples []string  `json:"errorSamples,omitempty"`
}
