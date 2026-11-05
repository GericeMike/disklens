package history

import (
	"fmt"
	"path/filepath"
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

// Compare 对比两份存档（裁剪树按路径对齐），返回 |Diff| >= minBytes 的变化。
// 后代路径的变化已被祖先概括（父目录总量含全部子树），折叠为最浅一条；
// 盘根（C:\ 等）只展示自身、不折叠他人，否则整盘变化会只剩一行没法定位。
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
	// 浅路径优先：祖先保留后，其所有后代变化折叠掉
	sort.Slice(out, func(i, j int) bool {
		di, dj := depthOf(out[i].Path), depthOf(out[j].Path)
		if di != dj {
			return di < dj
		}
		return abs(out[i].Diff) > abs(out[j].Diff)
	})
	kept := make([]Delta, 0, len(out))
	keptSet := map[string]bool{}
	for _, d := range out {
		p := strings.ToLower(filepath.Clean(d.Path))
		if isDriveRoot(p) {
			kept = append(kept, d) // 盘根展示，但不作为折叠锚点
			continue
		}
		covered := false
		cur := filepath.Dir(p)
		for cur != p && len(cur) > 3 { // 收缩到盘根即止；循环条件只挡首层
			if keptSet[strings.ToLower(cur)] {
				covered = true
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
		if covered {
			continue
		}
		keptSet[p] = true
		kept = append(kept, d)
	}
	return kept, nil
}

func depthOf(p string) int {
	return strings.Count(p, `\`)
}

func isDriveRoot(p string) bool {
	v := filepath.VolumeName(p)
	return v != "" && strings.EqualFold(p, v+`\`)
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
		// 去尾分隔符再拼，避免 C:\\Users 式双反斜杠键
		base := strings.TrimSuffix(r.Name, `\`)
		for _, c := range r.Children {
			walk(c, base)
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
