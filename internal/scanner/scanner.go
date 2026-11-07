package scanner

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// fileAttributeReparsePoint junction/符号链接属性：跳过以防目录循环与重复计数
const fileAttributeReparsePoint = 0x400

// topFilesKeep 大文件榜保留数量
const topFilesKeep = 100

// Options 扫描选项
type Options struct {
	Roots    []string // 扫描根列表，如 C:\ D:\
	Workers  int      // 并发 worker 数，0=自动
	Progress func(files, dirs int64, bytes int64)

	// Incremental 增量模式：叶子目录且目录修改时间与上次一致时整树复用上次结果，
	// 不再递归遍历。结构变化（新增/删除/改名）在任意深度都必然更新其直接父目录的
	// mtime，因此都能检出；盲区仅剩「叶子目录内文件被原地改写大小」（如数据库
	// WAL、日志追加），调用方应在 UI 注明并提供完整扫描开关。
	// 无可用历史（Prev=nil）时自动退化为全量扫描。
	Incremental bool
	Prev        *Result // 上次扫描结果（须与本次 Roots 完全同范围），由调用方加载
}

type fileHeap []FileInfo

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(FileInfo)) }
func (h *fileHeap) Pop() any          { old := *h; n := len(old); it := old[n-1]; *h = old[:n-1]; return it }

// taskQueue 无界任务队列（sync.Cond），worker 既是生产者也是消费者，
// 有界 channel 会因"全员阻塞在发送"而死锁，故必须无界。
type taskQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*nodeState
	closed bool
}

func newTaskQueue() *taskQueue {
	q := &taskQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *taskQueue) push(ns *nodeState) {
	q.mu.Lock()
	q.items = append(q.items, ns)
	q.cond.Signal()
	q.mu.Unlock()
}

func (q *taskQueue) pop() (*nodeState, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	return it, true
}

func (q *taskQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

type nodeState struct {
	node      *Node
	parent    *nodeState
	pending   int32   // 未完成子目录数
	ownDone   int32   // 本目录条目处理完成标记
	completed int32   // 完成级联已执行标记（CAS 防重）
	histMu    sync.Mutex
	hist      []int64 // 子树类型字节直方图（完成时快照进 node.Cat）
	histF     []int64 // 子树类型文件数直方图（完成时快照进 node.CatF）
}

// mergeHist 子树直方图并入本节点（子完成级联 / 嫁接 / 本地文件汇总共用）
func (ns *nodeState) mergeHist(cb, cf []int64) {
	if len(cb) == 0 && len(cf) == 0 {
		return
	}
	ns.histMu.Lock()
	defer ns.histMu.Unlock()
	if len(ns.hist) == 0 {
		ns.hist = make([]int64, catCount)
	}
	if len(ns.histF) == 0 {
		ns.histF = make([]int64, catCount)
	}
	for i := 0; i < catCount; i++ {
		if i < len(cb) {
			ns.hist[i] += cb[i]
		}
		if i < len(cf) {
			ns.histF[i] += cf[i]
		}
	}
}

type shardStat struct {
	mu    sync.Mutex
	bytes map[string]int64
	files map[string]int64
}

type scanner struct {
	opt       Options
	queue     *taskQueue
	inFlight  atomic.Int64 // 未完成目录数；归零即关闭队列
	wg        sync.WaitGroup
	shards    []*shardStat
	roots     []*Node
	heapMu    sync.Mutex
	top       fileHeap
	totalBytes, totalFiles, totalDirs, errors atomic.Int64
	errMu     sync.Mutex
	errSamples []string
	prevIdx   map[string]*Node // 增量模式：上次扫描的目录索引（小写路径 → 节点）
	graftMu   sync.Mutex
	grafted   []string // 被嫁接的子树路径（小写），供 TopFiles 从上次结果回收
}

// Scan 执行扫描，返回完整内存结果（未裁剪的树）。
func Scan(opt Options) (*Result, error) {
	if len(opt.Roots) == 0 {
		return nil, fmt.Errorf("未指定扫描范围")
	}
	if opt.Workers <= 0 {
		opt.Workers = 8
	}
	// 根必须存在：拼错路径的静默空扫描比报错更误导
	rootMTime := map[string]int64{}
	for _, r := range opt.Roots {
		p := normalizeRoot(r)
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("扫描根不存在或无法访问: %s", r)
		}
		rootMTime[p] = fi.ModTime().UnixNano()
	}
	s := &scanner{opt: opt, queue: newTaskQueue(), top: make(fileHeap, 0, topFilesKeep)}
	// 增量模式：为上次结果建目录索引（小写路径 → 节点）。找不到的路径照常遍历。
	if opt.Incremental && opt.Prev != nil {
		s.prevIdx = map[string]*Node{}
		var indexTree func(n *Node)
		indexTree = func(n *Node) {
			base := strings.TrimSuffix(n.Path0, `\`)
			for _, c := range n.Children {
				key := strings.ToLower(base + `\` + c.Name)
				s.prevIdx[key] = c
				indexTree(c)
			}
		}
		for _, r := range opt.Prev.Roots {
			indexTree(r)
		}
	}
	for i := 0; i < opt.Workers; i++ {
		s.shards = append(s.shards, &shardStat{bytes: map[string]int64{}, files: map[string]int64{}})
	}
	heap.Init(&s.top)

	res := &Result{StartedAt: time.Now()}
	res.Drives = probeDrives(opt.Roots)

	progressDone := make(chan struct{})
	if opt.Progress != nil {
		go func() {
			t := time.NewTicker(200 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					opt.Progress(s.totalFiles.Load(), s.totalDirs.Load(), s.totalBytes.Load())
				case <-progressDone:
					return
				}
			}
		}()
	}
	// 停滞看门狗：90 秒无任何文件进展 → 转储全部 goroutine 堆栈并退出（死锁自诊断）
	go func() {
		lastFiles, stallTicks := int64(-1), 0
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-progressDone:
				return
			case <-t.C:
				cur := s.totalFiles.Load()
				if cur == lastFiles {
					stallTicks++
				} else {
					stallTicks, lastFiles = 0, cur
				}
				if stallTicks >= 6 {
					buf := make([]byte, 1<<20)
					n := runtime.Stack(buf, true)
					fmt.Fprintf(os.Stderr, "\n[盘镜] 扫描停滞 90 秒，转储堆栈诊断:\n%s\n", buf[:n])
					os.Exit(3)
				}
			}
		}
	}()

	for _, root := range opt.Roots {
		p := normalizeRoot(root)
		rn := &Node{Name: p, Path0: p, MTime: rootMTime[p]}
		s.roots = append(s.roots, rn)
		s.inFlight.Add(1)
		s.queue.push(&nodeState{node: rn})
	}
	for i := 0; i < opt.Workers; i++ {
		s.wg.Add(1)
		go func(idx int) {
			defer s.wg.Done()
			sh := s.shards[idx]
			for {
				ns, ok := s.queue.pop()
				if !ok {
					return
				}
				s.processDir(ns, sh)
			}
		}(i)
	}
	s.wg.Wait()
	if opt.Progress != nil {
		close(progressDone)
	}
	// 增量模式：被嫁接的子树内文件没有经过本次遍历，其大文件榜条目从上次结果回收——
	// 嫁接的前提是目录 mtime 未变，其中文件集必然与上次一致，条目仍然准确。
	if opt.Incremental && opt.Prev != nil && len(s.grafted) > 0 {
		for _, fi := range opt.Prev.TopFiles {
			lp := strings.ToLower(fi.Path)
			for _, g := range s.grafted {
				if lp == g || strings.HasPrefix(lp, g+`\`) {
					s.pushTop(fi)
					break
				}
			}
		}
	}

	res.FinishedAt = time.Now()
	res.Duration = res.FinishedAt.Sub(res.StartedAt).Seconds()
	res.TotalBytes = s.totalBytes.Load()
	res.TotalFiles = s.totalFiles.Load()
	res.TotalDirs = s.totalDirs.Load()
	res.Errors = s.errors.Load()
	res.ErrorSamples = s.errSamples
	res.Roots = s.roots
	res.TopFiles = []FileInfo(s.top)
	sort.Slice(res.TopFiles, func(i, j int) bool { return res.TopFiles[i].Size > res.TopFiles[j].Size })
	res.TypeStats = s.mergeStats()
	sort.Slice(res.TypeStats, func(i, j int) bool { return res.TypeStats[i].Bytes > res.TypeStats[j].Bytes })
	for _, r := range res.Roots {
		sortChildren(r)
	}
	return res, nil
}

func (s *scanner) processDir(ns *nodeState, sh *shardStat) {
	entries, err := os.ReadDir(ns.node.Path0)
	if err != nil {
		// 无法访问（权限等）：按"空目录完成"处理。若不置 ownDone，
		// tryComplete 会直接 return，本节点 pending 卡死，整条祖先链不再级联，
		// 表现为中间目录（如 C:\Users）丢失全部子树字节。
		s.noteError(err, ns.node.Path0)
		atomic.StoreInt32(&ns.ownDone, 1)
		s.tryComplete(ns)
		if s.inFlight.Add(-1) == 0 {
			s.queue.close()
		}
		return
	}
	s.totalDirs.Add(1)

	var children []*nodeState
	var localBytes, localFiles int64
	localHist := make([]int64, catCount)
	localHistF := make([]int64, catCount)
	// 根节点 Path0 以反斜杠结尾（C:\），直接拼会产生 C:\\Users 双反斜杠，先去尾
	base := strings.TrimSuffix(ns.node.Path0, `\`)
	for _, e := range entries {
		full := base + "\\" + e.Name()
		if e.IsDir() {
			// junction/符号链接跳过（防循环、防重复计数）
			var info os.FileInfo
			if i, ierr := e.Info(); ierr == nil {
				info = i
				if attrs, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attrs.FileAttributes&fileAttributeReparsePoint != 0 {
					continue
				}
			} else {
				s.noteError(ierr, full)
				continue
			}
			// 增量嫁接：上次扫描时该目录是叶子（无子目录）且目录 mtime 至今未变
			// → 其文件集与上次完全一致，整树复用，不再递归。结构变化（增/删/改名）
			// 必然更新直接父目录 mtime，因此在任意深度都能检出，见 Options.Incremental。
			if s.prevIdx != nil {
				if pn := s.prevIdx[strings.ToLower(full)]; pn != nil && len(pn.Children) == 0 &&
					pn.MTime != 0 && pn.MTime == info.ModTime().UnixNano() && len(pn.Cat) > 0 {
					fillPath0(pn, full)
					ns.node.Children = append(ns.node.Children, pn)
					atomic.AddInt64(&ns.node.Bytes, pn.Bytes)
					atomic.AddInt64(&ns.node.Files, pn.Files)
					atomic.AddInt64(&ns.node.Dirs, pn.Dirs+1)
					ns.mergeHist(pn.Cat, pn.CatF)
					s.totalBytes.Add(pn.Bytes)
					s.totalFiles.Add(pn.Files)
					s.totalDirs.Add(pn.Dirs + 1)
					s.heapMu.Lock()
					s.grafted = append(s.grafted, strings.ToLower(full))
					s.heapMu.Unlock()
					continue
				}
			}
			child := &Node{Name: e.Name(), Path0: full, MTime: info.ModTime().UnixNano()}
			ns.node.Children = append(ns.node.Children, child)
			atomic.AddInt32(&ns.pending, 1)
			children = append(children, &nodeState{node: child, parent: ns})
		} else if e.Type().IsRegular() {
			info, ierr := e.Info()
			if ierr != nil {
				s.noteError(ierr, full)
				continue
			}
			sz := info.Size()
			localBytes += sz
			localFiles++
			s.totalBytes.Add(sz)
			s.totalFiles.Add(1)
			cat := categoryOf(strings.ToLower(filepath.Ext(e.Name())))
			localHist[catIndexOf(cat)] += sz
			localHistF[catIndexOf(cat)]++
			sh.mu.Lock()
			sh.bytes[cat] += sz
			sh.files[cat]++
			sh.mu.Unlock()
			s.pushTop(FileInfo{Path: full, Size: sz, ModTime: info.ModTime()})
		}
	}
	ns.mergeHist(localHist, localHistF)
	// 子任务入无界队列（不阻塞）
	for _, cs := range children {
		s.inFlight.Add(1)
		s.queue.push(cs)
	}
	atomic.AddInt64(&ns.node.Bytes, localBytes)
	atomic.AddInt64(&ns.node.Files, localFiles)
	atomic.StoreInt32(&ns.ownDone, 1)
	s.tryComplete(ns)
	// 本目录单元彻底完成：递减 in-flight，归零即关闭队列
	if s.inFlight.Add(-1) == 0 {
		s.queue.close()
	}
}

// tryComplete 目录完全聚合后（自身条目处理完且全部子目录完成）把最终值一次性上交父节点，
// 并在父节点随之完成时级联。CAS 保证每个节点恰好上交一次——既不遗漏也不重复。
func (s *scanner) tryComplete(ns *nodeState) {
	if atomic.LoadInt32(&ns.ownDone) != 1 || atomic.LoadInt32(&ns.pending) != 0 {
		return
	}
	if !atomic.CompareAndSwapInt32(&ns.completed, 0, 1) {
		return
	}
	// 直方图随完成级联搬运：快照进本节点（供增量嫁接复用），再并入父节点
	ns.histMu.Lock()
	if anyNonZero(ns.hist) {
		ns.node.Cat = ns.hist
	}
	if anyNonZero(ns.histF) {
		ns.node.CatF = ns.histF
	}
	cb, cf := ns.hist, ns.histF
	ns.hist, ns.histF = nil, nil
	ns.histMu.Unlock()
	p := ns.parent
	if p == nil {
		return
	}
	atomic.AddInt64(&p.node.Bytes, atomic.LoadInt64(&ns.node.Bytes))
	atomic.AddInt64(&p.node.Files, atomic.LoadInt64(&ns.node.Files))
	atomic.AddInt64(&p.node.Dirs, atomic.LoadInt64(&ns.node.Dirs)+1)
	p.mergeHist(cb, cf)
	if atomic.AddInt32(&p.pending, -1) == 0 {
		s.tryComplete(p)
	}
}

func anyNonZero(v []int64) bool {
	for _, x := range v {
		if x != 0 {
			return true
		}
	}
	return false
}

// fillPath0 给嫁接进来的历史子树（不含 Path0）重建完整路径链
func fillPath0(n *Node, prefix string) {
	n.Path0 = prefix
	base := strings.TrimSuffix(prefix, `\`)
	for _, c := range n.Children {
		fillPath0(c, base+`\`+c.Name)
	}
}

func (s *scanner) noteError(err error, path string) {
	if s.errors.Add(1) <= 10 {
		s.errMu.Lock()
		s.errSamples = append(s.errSamples, path+": "+err.Error())
		s.errMu.Unlock()
	}
}

func (s *scanner) pushTop(fi FileInfo) {
	s.heapMu.Lock()
	if len(s.top) < topFilesKeep {
		heap.Push(&s.top, fi)
	} else if fi.Size > s.top[0].Size {
		s.top[0] = fi
		heap.Fix(&s.top, 0)
	}
	s.heapMu.Unlock()
}

func (s *scanner) mergeStats() []TypeStat {
	// 类型统计 = 各根节点子树直方图求和。直方图包含遍历到的文件与增量嫁接的
	// 子树（嫁接部分从上次存档按桶搬运），因此增量模式的统计依然完整。
	// （shards 里的实时计数是旧通道，为兼容保留维护，但不再参与汇总。）
	merged := map[string]*TypeStat{}
	add := func(cat string, b, f int64) {
		m := merged[cat]
		if m == nil {
			m = &TypeStat{Category: cat}
			merged[cat] = m
		}
		m.Bytes += b
		m.Files += f
	}
	for _, r := range s.roots {
		for i, cat := range CatOrder {
			var b, f int64
			if i < len(r.Cat) {
				b = r.Cat[i]
			}
			if i < len(r.CatF) {
				f = r.CatF[i]
			}
			if b != 0 || f != 0 {
				add(cat, b, f)
			}
		}
	}
	out := make([]TypeStat, 0, len(merged))
	for _, v := range merged {
		out = append(out, *v)
	}
	return out
}

func sortChildren(n *Node) {
	if len(n.Children) == 0 {
		return
	}
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Bytes > n.Children[j].Bytes })
	for _, c := range n.Children {
		sortChildren(c)
	}
}

// probeDrives 查询各根所在盘的容量与剩余（扫描时点快照）
func probeDrives(roots []string) []DriveInfo {
	var out []DriveInfo
	for _, r := range roots {
		letter := volumeRoot(r)
		var free, total uint64
		if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(letter), nil, &total, &free); err == nil {
			out = append(out, DriveInfo{Letter: letter, TotalBytes: total, FreeBytes: free})
		}
	}
	return out
}

func volumeRoot(root string) string {
	vol := filepath.VolumeName(root)
	if vol == "" {
		return root
	}
	return vol + `\`
}

// NormalizeRoot 归一化扫描根（导出供历史比对范围用）：盘符补根反斜杠，相对路径转绝对
func NormalizeRoot(arg string) string {
	return normalizeRoot(arg)
}

// normalizeRoot 归一化扫描根：盘符补根反斜杠，相对路径转绝对
func normalizeRoot(arg string) string {
	arg = filepath.Clean(strings.Trim(arg, `"`))
	if vol := filepath.VolumeName(arg); vol != "" && len(arg) == len(vol) {
		return vol + `\` // "C:" 或 "C:\" → 盘根
	}
	if !filepath.IsAbs(arg) {
		if abs, err := filepath.Abs(arg); err == nil {
			return abs
		}
	}
	return arg
}

// ListFixedDrives 枚举本机固定磁盘根（DRIVE_FIXED）
func ListFixedDrives() []string {
	mask, _ := windows.GetLogicalDrives()
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + `:\`
		if windows.GetDriveType(windows.StringToUTF16Ptr(letter)) == windows.DRIVE_FIXED {
			out = append(out, letter)
		}
	}
	return out
}
