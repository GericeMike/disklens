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
	pending   int32 // 未完成子目录数
	ownDone   int32 // 本目录条目处理完成标记
	completed int32 // 完成级联已执行标记（CAS 防重）
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
	for _, r := range opt.Roots {
		if _, err := os.Stat(normalizeRoot(r)); err != nil {
			return nil, fmt.Errorf("扫描根不存在或无法访问: %s", r)
		}
	}
	s := &scanner{opt: opt, queue: newTaskQueue(), top: make(fileHeap, 0, topFilesKeep)}
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
		rn := &Node{Name: p, Path0: p}
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
	// 根节点 Path0 以反斜杠结尾（C:\），直接拼会产生 C:\\Users 双反斜杠，先去尾
	base := strings.TrimSuffix(ns.node.Path0, `\`)
	for _, e := range entries {
		full := base + "\\" + e.Name()
		if e.IsDir() {
			// junction/符号链接跳过（防循环、防重复计数）
			if info, ierr := e.Info(); ierr == nil {
				if attrs, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attrs.FileAttributes&fileAttributeReparsePoint != 0 {
					continue
				}
			} else {
				s.noteError(ierr, full)
				continue
			}
			child := &Node{Name: e.Name(), Path0: full}
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
			sh.mu.Lock()
			sh.bytes[cat] += sz
			sh.files[cat]++
			sh.mu.Unlock()
			s.pushTop(FileInfo{Path: full, Size: sz, ModTime: info.ModTime()})
		}
	}
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
	p := ns.parent
	if p == nil {
		return
	}
	atomic.AddInt64(&p.node.Bytes, atomic.LoadInt64(&ns.node.Bytes))
	atomic.AddInt64(&p.node.Files, atomic.LoadInt64(&ns.node.Files))
	atomic.AddInt64(&p.node.Dirs, atomic.LoadInt64(&ns.node.Dirs)+1)
	if atomic.AddInt32(&p.pending, -1) == 0 {
		s.tryComplete(p)
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
	merged := map[string]*TypeStat{}
	for _, sh := range s.shards {
		sh.mu.Lock()
		for cat, b := range sh.bytes {
			m := merged[cat]
			if m == nil {
				m = &TypeStat{Category: cat}
				merged[cat] = m
			}
			m.Bytes += b
		}
		for cat, f := range sh.files {
			merged[cat].Files += f
		}
		sh.mu.Unlock()
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
