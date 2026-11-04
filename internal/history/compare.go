package history

import (
	"fmt"
	"sort"
	"strings"

	"disklens/internal/scanner"
)

// Delta 两次扫描间某路径的容量变化
type Delta struct {
	Path string
	Old  int64
	New  int64
	Diff int64 // New - Old
}

// Compare 对比两份存档（裁剪树按路径对齐），返回 |Diff| >= minBytes 的变化，按绝对值降序
func Compare(a, b *Snapshot, minBytes int64) ([]Delta, error) {
	ma := treeMap(a)
	mb := treeMap(b)
	seen := map[string]bool{}
	var out []Delta
	for path, nb := range mb {
		seen[path] = true
		ob := ma[path]
		if ob == nb {
			continue
		}
		d := Delta{Path: path, Old: ob, New: nb, Diff: nb - ob}
		if abs(d.Diff) >= minBytes {
			out = append(out, d)
		}
	}
	for path, ob := range ma {
		if seen[path] {
			continue
		}
		d := Delta{Path: path, Old: ob, New: 0, Diff: -ob}
		if abs(d.Diff) >= minBytes {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return abs(out[i].Diff) > abs(out[j].Diff) })
	return out, nil
}

// treeMap 把裁剪树摊平成 路径→字节
func treeMap(s *Snapshot) map[string]int64 {
	m := map[string]int64{}
	var walk func(n *scanner.Node, prefix string)
	walk = func(n *scanner.Node, prefix string) {
		path := prefix + "\\" + n.Name
		m[path] = n.Bytes
		for _, c := range n.Children {
			walk(c, path)
		}
	}
	for _, r := range s.Roots {
		m[r.Name] = r.Bytes // 根本身
		for _, c := range r.Children {
			walk(c, r.Name)
		}
	}
	return m
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ScopeMatch 两次存档范围是否一致（用于给出提示）
func ScopeMatch(a, b *Snapshot) bool {
	return strings.Join(a.Meta.Scope, ",") == strings.Join(b.Meta.Scope, ",")
}

var _ = fmt.Sprintf
