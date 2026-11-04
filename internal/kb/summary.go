package kb

import (
	"path/filepath"
	"sort"
	"strings"

	"disklens/internal/scanner"
)

// CleanSummaryEntry 可清理汇总里的一项（按知识条目聚合）
type CleanSummaryEntry struct {
	EntryID   string        `json:"entryId"`
	Name      string        `json:"name"`
	Cat       string        `json:"cat"`
	Risk      string        `json:"risk"`
	Why       string        `json:"why"`
	TotalSize int64         `json:"totalSize"`
	Paths     []CleanPath   `json:"paths"`
	Clean     []CleanItem   `json:"clean,omitempty"`
}

// CleanPath 汇总里的一条具体落盘点
type CleanPath struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Files int64  `json:"files"`
}

// BuildCleanSummary 遍历扫描树，把知识库命中的"安全/谨慎"目录聚合成可清理清单。
// minSize 过滤小目录；maxEntries 限制条数。
func BuildCleanSummary(res *scanner.Result, minSize int64, maxEntries int) []CleanSummaryEntry {
	if res == nil {
		return nil
	}
	agg := map[string]*CleanSummaryEntry{}
	var walk func(n *scanner.Node, prefix string, depth int)
	walk = func(n *scanner.Node, prefix string, depth int) {
		p := prefix + "\\" + n.Name
		if depth >= 1 && n.Bytes >= minSize && n.Files > 0 {
			if hits := Match(p); len(hits) > 0 {
				h := hits[0]
				counted := false
				if h.Entry.Risk == RiskSafe || h.Entry.Risk == RiskCaution {
					if h.Entry.NoSummary {
						// 解释性/容器条目：不计数，但继续下钻寻找内部的具体条目
					} else {
						e := agg[h.Entry.ID]
						if e == nil {
							e = &CleanSummaryEntry{
								EntryID: h.Entry.ID, Name: h.Entry.Name, Cat: h.Entry.Cat,
								Risk: h.Entry.Risk, Why: h.Entry.Why, Clean: h.Entry.Clean,
							}
							agg[h.Entry.ID] = e
						}
						// 最顶层命中即按"含全部内容"计入，不再下钻——子目录大小已含在本目录内
						e.TotalSize += n.Bytes
						if len(e.Paths) < 6 {
							e.Paths = append(e.Paths, CleanPath{Path: p, Size: n.Bytes, Files: n.Files})
						}
						counted = true
					}
				}
				if counted {
					return
				}
			}
		}
		if depth < 7 {
			for _, c := range n.Children {
				walk(c, p, depth+1)
			}
		}
	}
	for _, r := range res.Roots {
		rootPrefix := strings.TrimSuffix(r.Name, `\`)
		for _, c := range r.Children {
			walk(c, rootPrefix, 1)
		}
	}
	out := make([]CleanSummaryEntry, 0, len(agg))
	for _, e := range agg {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalSize > out[j].TotalSize })
	if len(out) > maxEntries {
		out = out[:maxEntries]
	}
	return out
}

// TreeIndex 全树路径索引（路径→节点），供 GUI 按路径取子树/大小
type TreeIndex map[string]*scanner.Node

// BuildTreeIndex 把完整扫描树摊平为路径索引
func BuildTreeIndex(res *scanner.Result) TreeIndex {
	idx := TreeIndex{}
	var walk func(n *scanner.Node, prefix string)
	walk = func(n *scanner.Node, prefix string) {
		p := prefix + "\\" + n.Name
		idx[strings.ToLower(p)] = n
		for _, c := range n.Children {
			walk(c, p)
		}
	}
	for _, r := range res.Roots {
		idx[strings.ToLower(r.Name)] = r
		rootPrefix := strings.TrimSuffix(r.Name, `\`)
		for _, c := range r.Children {
			walk(c, rootPrefix)
		}
	}
	return idx
}

// ChildrenOf 从索引取某路径的子目录列表（按大小降序）
func ChildrenOf(idx TreeIndex, path string) []CleanPath {
	n, ok := idx[strings.ToLower(filepath.Clean(path))]
	if !ok {
		return nil
	}
	out := make([]CleanPath, 0, len(n.Children))
	for _, c := range n.Children {
		out = append(out, CleanPath{Path: parentPath(path) + "\\" + c.Name, Size: c.Bytes, Files: c.Files})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

func parentPath(p string) string {
	i := strings.LastIndexAny(p, `\/`)
	if i <= 0 {
		return p
	}
	return p[:i]
}
