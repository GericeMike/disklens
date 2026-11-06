package scanner

import (
	"container/heap"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// catFileHeap 按 Size 的最小堆：Top-K 场景保大去小，内存 O(keep)
type catFileHeap []FileInfo

func (h catFileHeap) Len() int           { return len(h) }
func (h catFileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h catFileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *catFileHeap) Push(x any)        { *h = append(*h, x.(FileInfo)) }
func (h *catFileHeap) Pop() any          { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }

// CollectCategoryFiles 并发遍历 roots，收集扩展名属于 category 大类的文件，
// 返回按大小降序的前 keep 条。reparse 目录（junction/符号链接）跳过——
// 与扫描引擎同一规则，防循环、防重复计数。只读磁盘，不写任何数据。
//
// 并发模型与扫描引擎一致：固定 worker 数 + 无界目录队列（sync.Cond），
// pending 归零即遍历完成。勿改成「spawn 时占名额」的递归模式——
// 名额持有者再阻塞等名额会形成循环等待（真机踩过的死锁）。
func CollectCategoryFiles(roots []string, category string, keep int) []FileInfo {
	if category == "" || keep <= 0 || len(roots) == 0 {
		return nil
	}
	var hmu sync.Mutex
	h := &catFileHeap{}
	heap.Init(h)

	// 无界目录队列：pending = 已入队未处理的目录数，归零即完成
	var qmu sync.Mutex
	qcond := sync.NewCond(&qmu)
	var queue []string
	pending := 0
	done := false
	push := func(d string) {
		qmu.Lock()
		queue = append(queue, d)
		pending++
		qcond.Signal()
		qmu.Unlock()
	}
	for _, r := range roots {
		push(r)
	}

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 2 {
		workers = 2
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				qmu.Lock()
				for len(queue) == 0 && !done {
					qcond.Wait()
				}
				if len(queue) == 0 && done {
					qmu.Unlock()
					return
				}
				dir := queue[0]
				queue = queue[1:]
				qmu.Unlock()

				// ReadDir 是耗时操作，不持队列锁
				ents, err := os.ReadDir(dir)
				qmu.Lock()
				pending--
				if err == nil {
					for _, e := range ents {
						full := filepath.Join(dir, e.Name())
						if e.IsDir() {
							// junction/符号链接跳过（防循环、防重复计数）
							if info, ierr := e.Info(); ierr == nil {
								if attrs, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attrs.FileAttributes&fileAttributeReparsePoint != 0 {
									continue
								}
							} else {
								continue
							}
							queue = append(queue, full)
							pending++
							qcond.Signal()
							continue
						}
						if !e.Type().IsRegular() {
							continue
						}
						if CategoryOf(strings.ToLower(filepath.Ext(e.Name()))) != category {
							continue
						}
						info, ierr := e.Info()
						if ierr != nil {
							continue
						}
						fi := FileInfo{Path: full, Size: info.Size(), ModTime: info.ModTime()}
						hmu.Lock()
						if h.Len() < keep {
							heap.Push(h, fi)
						} else if h.Len() > 0 && fi.Size > (*h)[0].Size {
							(*h)[0] = fi
							heap.Fix(h, 0)
						}
						hmu.Unlock()
					}
				}
				if pending == 0 {
					done = true
					qcond.Broadcast()
				}
				qmu.Unlock()
			}
		}()
	}
	wg.Wait()

	out := make([]FileInfo, 0, h.Len())
	for h.Len() > 0 {
		out = append(out, heap.Pop(h).(FileInfo))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}
