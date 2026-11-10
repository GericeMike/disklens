package history

import (
	"strings"
	"testing"

	"disklens/internal/scanner"
)

// mkTree 构造最小快照：根 + 指定相对路径与字节量
func mkTree(root string, entries map[string]int64) *Snapshot {
	// 先建根，再按路径逐级挂节点
	rootNode := &scanner.Node{Name: root}
	byPath := map[string]*scanner.Node{root: rootNode}
	var ensure func(string) *scanner.Node
	ensure = func(dir string) *scanner.Node {
		if n, ok := byPath[dir]; ok {
			return n
		}
		if strings.HasSuffix(dir, ":") {
			return byPath[root] // 裸盘符 → 根节点
		}
		parent := ensure(parentDir(dir))
		n := &scanner.Node{Name: baseName(dir)}
		parent.Children = append(parent.Children, n)
		byPath[dir] = n
		return n
	}
	for path, bytes := range entries {
		n := ensure(path)
		n.Bytes = bytes
	}
	snap := &Snapshot{Roots: []*scanner.Node{rootNode}}
	// 回填 Bytes 聚合（父=子之和）
	var sum func(n *scanner.Node) int64
	sum = func(n *scanner.Node) int64 {
		total := n.Bytes
		for _, c := range n.Children {
			total += sum(c)
		}
		n.Bytes = total
		return total
	}
	sum(rootNode)
	return snap
}

func parentDir(p string) string {
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p // 裸盘符（C:）等，由 ensure 兜底
	}
	if i == 2 && len(p) >= 3 && p[1] == ':' {
		return p[:3] // 上级是盘根 C:\
	}
	return p[:i]
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// TestCompareFold 深层变化折叠为最浅祖先；盘根展示但不吞并他人
func TestCompareFold(t *testing.T) {
	old := mkTree(`C:\`, map[string]int64{
		`C:\netdisk`:                  100,
		`C:\netdisk\AppA`:             60,
		`C:\netdisk\AppA\DataB`:       60,
		`C:\keep`:                     10,
		`C:\appdata\.mimosa\hook-a`:   0,
		`C:\appdata\.mimosa\hook-b`:   0,
	})
	cur := mkTree(`C:\`, map[string]int64{
		`C:\netdisk`:                  35, // -65
		`C:\netdisk\AppA`:             0,  // -60（被祖先折叠）
		`C:\netdisk\AppA\DataB`:       0,  // -60（被祖先折叠）
		`C:\keep`:                     10, // 不变
		`C:\appdata\.mimosa\hook-a`:   40, // +40
		`C:\appdata\.mimosa\hook-b`:   0,  // 消失（bytes 0，无变化量，minBytes 过滤）
	})
	deltas, err := Compare(old, cur, 1)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	// 期望集合（顺序不敏感）：
	//   C:\netdisk —— AppA/DataB 深层变化被它折叠
	//   C:\ —— 盘根展示但不吞并他人
	//   C:\appdata —— hook-a 的变化被浅层祖先聚合概括
	want := map[string]bool{
		`C:\netdisk`:  true,
		`C:\`:         true,
		`C:\appdata`:  true,
	}
	got := map[string]bool{}
	for _, d := range deltas {
		got[d.Path] = true
		t.Logf("  %s  %d→%d (%+d)", d.Path, d.Old, d.New, d.Diff)
	}
	if len(deltas) != len(want) {
		t.Fatalf("折叠后应剩 %v 条，实际 %v 条 %+v", len(want), len(deltas), got)
	}
	for w := range want {
		if !got[w] {
			t.Errorf("缺少期望条目 %s，实际 %v", w, got)
		}
	}
	// 深层条目必须被折叠
	for _, bad := range []string{`C:\netdisk\AppA`, `C:\netdisk\AppA\DataB`, `C:\appdata\.mimosa\hook-a`} {
		if got[bad] {
			t.Errorf("%s 应被祖先折叠", bad)
		}
	}
}

// TestCompareDriveRootDoesNotSwallow 盘根单独变化时展示自身，但不折叠其它路径
func TestCompareDriveRootDoesNotSwallow(t *testing.T) {
	old := mkTree(`C:\`, map[string]int64{
		`C:\a`: 50,
		`C:\b`: 20,
	})
	// 手工设根 Bytes（模拟聚合）：根总量变化但 a/b 无一变化 → 不可能，除非外部文件
	// 这里直接改根的 Bytes 模拟 pagefile 等非目录占用变化
	old.Roots[0].Bytes = 80
	cur := mkTree(`C:\`, map[string]int64{
		`C:\a`: 50,
		`C:\b`: 20,
	})
	cur.Roots[0].Bytes = 100 // 根 +20，但 a/b 均不变
	deltas, err := Compare(old, cur, 1)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(deltas) != 1 || deltas[0].Path != `C:\` {
		t.Fatalf("应只展示盘根变化，实际 %+v", deltas)
	}
}
