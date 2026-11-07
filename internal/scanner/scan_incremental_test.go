package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func mkFile(t *testing.T, root, rel string, n int) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func statMap(res *Result) map[string]TypeStat {
	m := map[string]TypeStat{}
	for _, s := range res.TypeStats {
		m[s.Category] = s
	}
	return m
}

func containsPath(files []FileInfo, suffix string) bool {
	for _, f := range files {
		if filepath.Base(f.Path) == suffix {
			return true
		}
	}
	return false
}

func findNode(n *Node, name string) *Node {
	if n.Name == name {
		return n
	}
	for _, c := range n.Children {
		if r := findNode(c, name); r != nil {
			return r
		}
	}
	return nil
}

// TestIncrementalScanEquivalence 增量扫描结果必须与全量重扫完全一致：
// 结构变化（叶内加文件/删文件/非叶下加新叶子）全部检出，未变叶子整树嫁接，
// 类型统计（字节+文件数）与 TopFiles 均正确。
func TestIncrementalScanEquivalence(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, `a\f1.bin`, 10<<10)
	mkFile(t, root, `a\v1.mp4`, 20<<10)
	mkFile(t, root, `b\sub\big.bin`, 100<<10)
	mkFile(t, root, `e\keep.bin`, 50<<10)
	mkFile(t, root, `top.txt`, 3<<10)

	full1, err := Scan(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}

	// 结构变化：叶 a 加文件（a.mtime 变）· 叶 sub 删文件（sub.mtime 变）·
	// 非叶 sub 下加新叶子 d（sub.mtime 变）· 根加文件（根永远被遍历）
	mkFile(t, root, `a\f3.log`, 30<<10)
	if err := os.Remove(filepath.Join(root, `b\sub\big.bin`)); err != nil {
		t.Fatal(err)
	}
	mkFile(t, root, `b\sub\d\deep.bin`, 40<<10)
	mkFile(t, root, `top2.txt`, 4<<10)

	full2, err := Scan(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	inc, err := Scan(Options{Roots: []string{root}, Incremental: true, Prev: full1})
	if err != nil {
		t.Fatal(err)
	}

	if inc.TotalBytes != full2.TotalBytes {
		t.Errorf("总字节不一致: 增量=%d 全量=%d", inc.TotalBytes, full2.TotalBytes)
	}
	if inc.TotalFiles != full2.TotalFiles {
		t.Errorf("总文件数不一致: 增量=%d 全量=%d", inc.TotalFiles, full2.TotalFiles)
	}
	if inc.TotalDirs != full2.TotalDirs {
		t.Errorf("总目录数不一致: 增量=%d 全量=%d", inc.TotalDirs, full2.TotalDirs)
	}
	// 类型统计逐类对拍（字节+文件数）
	gotStats, wantStats := statMap(inc), statMap(full2)
	for cat, want := range wantStats {
		got, ok := gotStats[cat]
		if !ok {
			t.Errorf("增量缺少类型 %s", cat)
			continue
		}
		if got.Bytes != want.Bytes || got.Files != want.Files {
			t.Errorf("类型 %s 统计不一致: 增量(%d,%d) 全量(%d,%d)",
				cat, got.Bytes, got.Files, want.Bytes, want.Files)
		}
	}
	if len(gotStats) != len(wantStats) {
		t.Errorf("类型数不一致: 增量=%d 全量=%d", len(gotStats), len(wantStats))
	}
	// TopFiles：被删文件不得残留（sub 已变，不嫁接）；未变叶子的大文件必须由种子回收
	if containsPath(inc.TopFiles, "big.bin") {
		t.Errorf("已删除的 big.bin 残留在增量 TopFiles 中")
	}
	if !containsPath(inc.TopFiles, "keep.bin") {
		t.Errorf("未变叶子的 keep.bin 未被嫁接种子回收进 TopFiles")
	}
	// 未变叶子 e 整树复用：Bytes 精确等于 50KB
	e := findNode(inc.Roots[0], "e")
	if e == nil || e.Bytes != 50<<10 {
		t.Errorf("未变叶子 e 嫁接后字节不对: %+v", e)
	}
	if e != nil && len(e.Cat) == 0 {
		t.Errorf("嫁接节点缺少类型直方图")
	}
}

// TestIncrementalOldArchiveFallback 旧存档（无 MTime/Cat 字段）不满足嫁接条件，
// 增量模式自动退化为全量，结果与全量一致。
func TestIncrementalOldArchiveFallback(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, `a\f1.bin`, 10<<10)
	mkFile(t, root, `b\g.zip`, 20<<10)

	full1, err := Scan(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟旧格式：剥掉新字段
	var strip func(n *Node)
	strip = func(n *Node) {
		n.MTime = 0
		n.Cat = nil
		n.CatF = nil
		for _, c := range n.Children {
			strip(c)
		}
	}
	for _, r := range full1.Roots {
		strip(r)
	}
	mkFile(t, root, `a\f2.bin`, 5<<10) // 结构变化

	full2, err := Scan(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	inc, err := Scan(Options{Roots: []string{root}, Incremental: true, Prev: full1})
	if err != nil {
		t.Fatal(err)
	}
	if inc.TotalBytes != full2.TotalBytes || inc.TotalFiles != full2.TotalFiles {
		t.Errorf("旧存档退化后数字不一致: 增量(%d,%d) 全量(%d,%d)",
			inc.TotalBytes, inc.TotalFiles, full2.TotalBytes, full2.TotalFiles)
	}
}
