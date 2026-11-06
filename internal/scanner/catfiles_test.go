package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestCollectCategoryFiles 类型明细收集：过滤/排序/Top-K/忽略目录
func TestCollectCategoryFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(`a\big.mp4`, string(make([]byte, 300)))
	mk(`a\small.mp4`, string(make([]byte, 10)))
	mk(`b\mid.mp4`, string(make([]byte, 100)))
	mk(`b\note.txt`, "txt 不是视频")
	mk(`c\nest\deep.mkv`, string(make([]byte, 200)))
	mk(`c\top.mp4`, string(make([]byte, 50)))

	got := CollectCategoryFiles([]string{root}, "视频", 200)
	if len(got) != 5 {
		t.Fatalf("应命中 5 个视频文件，实际 %d: %+v", len(got), got)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Size > got[j].Size }) {
		t.Fatalf("结果应按大小降序: %+v", got)
	}
	if got[0].Size != 300 || got[4].Size != 10 {
		t.Fatalf("排序边界不对: %+v", got)
	}
	for _, f := range got {
		if filepath.Base(f.Path) == "note.txt" {
			t.Fatalf("混入了非该类型文件: %+v", f)
		}
	}

	// Top-K 截断：keep=2 只留最大的两个
	top2 := CollectCategoryFiles([]string{root}, "视频", 2)
	if len(top2) != 2 || top2[0].Size != 300 || top2[1].Size != 200 {
		t.Fatalf("Top-2 应为 300/200: %+v", top2)
	}

	// 空类型/空根：不出结果不 panic
	if r := CollectCategoryFiles([]string{root}, "", 10); len(r) != 0 {
		t.Fatalf("空类型应返回空: %+v", r)
	}
	if r := CollectCategoryFiles(nil, "视频", 10); len(r) != 0 {
		t.Fatalf("空根应返回空: %+v", r)
	}

	// 不存在的根：静默容忍
	if r := CollectCategoryFiles([]string{filepath.Join(root, "nope")}, "视频", 10); len(r) != 0 {
		t.Fatalf("不存在的根应返回空: %+v", r)
	}
}

// TestCollectCategoryFilesStress 深宽混合目录树全量收集（旧「spawn 占名额」实现在此规模会死锁）
func TestCollectCategoryFilesStress(t *testing.T) {
	root := t.TempDir()
	want := 0
	mk := func(rel string, n int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 10 层深 × 每层 5 个目录分支 + 每层 1 个日志文件 = 50 个目录 + 10 个文件
	path := ""
	for d := 0; d < 10; d++ {
		path = filepath.Join(path, "d"+string(rune('a'+d)))
		for b := 0; b < 5; b++ {
			mk(filepath.Join(path, "b"+string(rune('a'+b)), "x.log"), b+1)
			want++
		}
		mk(filepath.Join(path, "f.log"), 100)
		want++
	}
	got := CollectCategoryFiles([]string{root}, "日志", 200)
	if len(got) != want {
		t.Fatalf("应命中 %d 个日志文件，实际 %d", want, len(got))
	}
}

// TestCollectCategoryFilesJunction junction 目录跳过，不重复收集
func TestCollectCategoryFilesJunction(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "v.mp4"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	// 目录符号链接同样带 reparse 位（mklink /J 的 stdlib 替代，避免 exec）
	if err := os.Symlink(real, link); err != nil {
		t.Skip("无法创建目录符号链接（需开发者模式或管理员），跳过该用例:", err)
	}
	got := CollectCategoryFiles([]string{root}, "视频", 10)
	// real 与 link 指向同一目录；reparse 跳过后只应命中 real 下那一份
	if len(got) != 1 {
		t.Fatalf("junction/符号链接 跳过失败，命中 %d 条: %+v", len(got), got)
	}
	if filepath.Base(filepath.Dir(got[0].Path)) != "real" {
		t.Fatalf("不应从链接内收集: %+v", got)
	}
}
