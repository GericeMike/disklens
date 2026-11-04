// Package cli — 盘镜 DiskLens 命令行入口
package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"disklens/internal/history"
	"disklens/internal/scanner"
)

// Run 解析并执行子命令，返回进程退出码
func Run(args []string) int {
	switch args[0] {
	case "scan":
		return cmdScan(args[1:])
	case "compare":
		return cmdCompare(args[1:])
	case "list":
		return cmdList()
	case "help", "-h", "--help":
		printHelp()
		return 0
	default:
		fmt.Printf("未知命令: %s\n\n", args[0])
		printHelp()
		return 2
	}
}

func printHelp() {
	fmt.Print(`盘镜 DiskLens — 磁盘空间分析器

用法:
  disklens                     打开图形界面
  disklens scan [路径...]      扫描（默认: 全部固定磁盘），结果存入历史
  disklens compare             对比最近两次扫描，看谁长大了
  disklens list                列出历史扫描

示例:
  disklens scan C:\ D:\
  disklens compare --min-gb 1
`)
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

func signBytes(d int64) string {
	s := humanBytes(abs2(d))
	if d > 0 {
		return "+" + s
	}
	if d < 0 {
		return "-" + s
	}
	return s
}

func abs2(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func cmdScan(argv []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	workers := fs.Int("workers", 8, "并发 worker 数")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	roots := fs.Args()
	if len(roots) == 0 {
		roots = scanner.ListFixedDrives()
		if len(roots) == 0 {
			fmt.Println("未找到固定磁盘")
			return 1
		}
	}
	fmt.Printf("盘镜 DiskLens · 开始扫描: %s\n", strings.Join(roots, "  "))
	started := time.Now()

	var progressLine string
	res, err := scanner.Scan(scanner.Options{
		Roots:   roots,
		Workers: *workers,
		Progress: func(files, dirs int64, bytes int64) {
			line := fmt.Sprintf("\r  已扫描 %s 目录 · %s 文件 · %s",
				comma(dirs), comma(files), humanBytes(bytes))
			if len(line) < len(progressLine) {
				line += strings.Repeat(" ", len(progressLine)-len(line))
			}
			progressLine = line
			fmt.Print(line)
		},
	})
	if err != nil {
		fmt.Println("\n扫描失败:", err)
		return 1
	}
	fmt.Print("\r" + strings.Repeat(" ", 100) + "\r")

	fmt.Printf("扫描完成 · 耗时 %s · %s 目录 · %s 文件 · 共 %s · %d 处无法访问已跳过\n\n",
		time.Duration(res.Duration*float64(time.Second)).Round(time.Second),
		comma(res.TotalDirs), comma(res.TotalFiles), humanBytes(res.TotalBytes), res.Errors)

	for _, d := range res.Drives {
		usedPct := 0.0
		if d.TotalBytes > 0 {
			usedPct = float64(d.TotalBytes-d.FreeBytes) / float64(d.TotalBytes) * 100
		}
		fmt.Printf("  %s  总 %s · 剩 %s (%.1f%% 已用)\n",
			d.Letter, humanBytes(int64(d.TotalBytes)), humanBytes(int64(d.FreeBytes)), usedPct)
	}
	fmt.Println()

	// Top 文件夹：从完整树取前 15
	type row struct{ path string; bytes int64 }
	var rows []row
	var walk func(n *scanner.Node, prefix string)
	walk = func(n *scanner.Node, prefix string) {
		p := prefix + "\\" + n.Name
		if prefix != "" && n.Files > 0 {
			rows = append(rows, row{p, n.Bytes})
		}
		for _, c := range n.Children {
			walk(c, p)
		}
	}
	for _, r := range res.Roots {
		for _, c := range r.Children {
			walk(c, r.Name)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].bytes > rows[j].bytes })
	fmt.Println("▶ Top 15 文件夹")
	for i, r := range rows {
		if i == 15 {
			break
		}
		fmt.Printf("  %2d. %-70s %10s\n", i+1, r.path, humanBytes(r.bytes))
	}
	fmt.Println()

	fmt.Println("▶ Top 15 大文件")
	for i, f := range res.TopFiles {
		if i == 15 {
			break
		}
		fmt.Printf("  %2d. %-70s %10s\n", i+1, f.Path, humanBytes(f.Size))
	}
	fmt.Println()

	fmt.Println("▶ 文件类型统计")
	for _, t := range res.TypeStats {
		if t.Files == 0 {
			continue
		}
		fmt.Printf("  %-10s %12s  %s 个\n", t.Category, humanBytes(t.Bytes), comma(t.Files))
	}
	fmt.Println()

	path, err := history.Save(res)
	if err != nil {
		fmt.Println("历史存档失败:", err)
		return 1
	}
	fmt.Println("已存档:", path)
	if len(res.ErrorSamples) > 0 {
		fmt.Println("无法访问示例(最多10条):")
		for _, s := range res.ErrorSamples {
			fmt.Println("  ·", s)
		}
	}
	_ = started
	return 0
}

func cmdCompare(argv []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	minGB := fs.Float64("min-gb", 0.5, "变化量阈值(GB)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	names, err := history.List()
	if err != nil {
		fmt.Println("读取历史失败:", err)
		return 1
	}
	if len(names) < 2 {
		fmt.Println("历史不足两份，先运行 scan")
		return 1
	}
	a, err := history.LoadByName(names[1])
	if err != nil {
		fmt.Println("读取存档失败:", err)
		return 1
	}
	b, err := history.LoadByName(names[0])
	if err != nil {
		fmt.Println("读取存档失败:", err)
		return 1
	}
	if !history.ScopeMatch(a, b) {
		fmt.Printf("注意: 两次扫描范围不同 (%v → %v)\n", a.Meta.Scope, b.Meta.Scope)
	}
	minBytes := int64(*minGB * 1024 * 1024 * 1024)
	deltas, err := history.Compare(a, b, minBytes)
	if err != nil {
		fmt.Println("对比失败:", err)
		return 1
	}
	fmt.Printf("对比: %s → %s (阈值 ≥ %.1f GB)\n\n",
		a.Meta.FinishedAt.Format("2006-01-02 15:04"), b.Meta.FinishedAt.Format("2006-01-02 15:04"), *minGB)
	if len(deltas) == 0 {
		fmt.Println("没有超过阈值的变化")
		return 0
	}
	fmt.Printf("  %-64s %12s %12s %12s\n", "路径", "上次", "本次", "变化")
	for i, d := range deltas {
		if i == 25 {
			fmt.Printf("  … 还有 %d 项，可用 --min-gb 提高阈值\n", len(deltas)-25)
			break
		}
		fmt.Printf("  %-64s %12s %12s %12s\n", d.Path, humanBytes(d.Old), humanBytes(d.New), signBytes(d.Diff))
	}
	return 0
}

func cmdList() int {
	names, err := history.List()
	if err != nil {
		fmt.Println("读取历史失败:", err)
		return 1
	}
	if len(names) == 0 {
		fmt.Println("暂无历史存档，先运行 scan")
		return 0
	}
	fmt.Printf("  %-3s %-18s %-14s %12s %8s\n", "#", "时间", "范围", "总计", "耗时")
	for i, name := range names {
		snap, err := history.LoadByName(name)
		if err != nil {
			fmt.Printf("  %-3d %s (读取失败)\n", i+1, name)
			continue
		}
		fmt.Printf("  %-3d %-18s %-14s %12s %7.0fs\n",
			i+1,
			snap.Meta.FinishedAt.Format("2006-01-02 15:04"),
			strings.Join(snap.Meta.Scope, ","),
			humanBytes(snap.Meta.Totals.Bytes),
			snap.Meta.DurationSec)
	}
	_ = filepath.Separator
	return 0
}

func comma(v int64) string {
	s := fmt.Sprintf("%d", v)
	if v < 1000 {
		return s
	}
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return s + "," + strings.Join(out, ",")
}
