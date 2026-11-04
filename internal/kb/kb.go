// Package kb — 盘镜 DiskLens 本地知识库：
// 内嵌策划条目 + 用户扩展文件，把"这个文件夹是干什么的/能不能删/同一软件还散落在哪"讲清楚。
package kb

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed kb.json
var embeddedJSON []byte

// 风险等级
const (
	RiskSafe    = "safe"    // 🟢 可安全清理
	RiskCaution = "caution" // 🟡 需谨慎：看清子项再动手
	RiskDanger  = "danger"  // 🔴 危险：程序本体/系统组件，不要删
	RiskData    = "data"    // 📁 个人数据：工具不替你判断
	RiskInfo    = "info"    // ℹ️ 说明性条目（系统机制文件）
)

// CleanItem 条目内可单独清理的子目录
type CleanItem struct {
	Sub  string `json:"sub"`  // 相对条目路径的子路径，或 "**/..." 任意位置
	Risk string `json:"risk"` // safe / caution
	Note string `json:"note"`
}

// RelatedPath 广度连接：同一软件/主题的其他落盘点
type RelatedPath struct {
	Paths []string `json:"paths"`
	Note  string   `json:"note,omitempty"`
}

// Entry 一条知识
type Entry struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Cat     string        `json:"cat"`
	Desc    string        `json:"desc"`   // 这是干什么的
	Risk    string        `json:"risk"`   // safe/caution/danger/data/info
	Why     string        `json:"why"`    // 为什么是这个风险等级
	Paths   []string      `json:"paths"`  // 落盘点模式（支持 %ENV% 与 **/ 前缀）
	Clean   []CleanItem   `json:"clean,omitempty"`
	Related []RelatedPath `json:"related,omitempty"`
}

type database struct {
	Version int      `json:"version"`
	Entries []*Entry `json:"entries"`
}

// Hit 一次匹配结果
type Hit struct {
	Entry       *Entry
	MatchedPath string   // 触发匹配的模式展开后的落盘点（最具体者）
	Exists      []string // 该条目在本机上真实存在的落盘点（广度连接）
}

type compiled struct {
	entry     *Entry
	expanded  []string // 已展开的普通前缀模式
	anywhere  []string // "**/xxx" 的 xxx 部分（"\a\b" 形态）
	matchPath string   // 匹配命中的落盘点
}

type store struct {
	mu      sync.RWMutex
	entries []*compiled
	byID    map[string]*Entry
}

var std *store
var stdOnce sync.Once

// Load 返回知识库单例：内嵌条目 + %APPDATA%\DiskLens\kb-user.json 用户扩展
func Load() *store {
	stdOnce.Do(func() {
		std = newStore()
		var db database
		if err := json.Unmarshal(embeddedJSON, &db); err == nil {
			std.add(db.Entries)
		}
		// 用户扩展（可选）：追加或按 id 覆盖
		if base, err := os.UserConfigDir(); err == nil {
			if raw, err := os.ReadFile(filepath.Join(base, "DiskLens", "kb-user.json")); err == nil {
				var udb database
				if err := json.Unmarshal(raw, &udb); err == nil {
					std.add(udb.Entries)
				}
			}
		}
	})
	return std
}

func newStore() *store {
	return &store{byID: map[string]*Entry{}}
}

func (st *store) add(entries []*Entry) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, e := range entries {
		c := compile(e)
		if c == nil {
			continue
		}
		if old, ok := st.byID[e.ID]; ok {
			// 用户扩展覆盖同 id 内嵌条目
			for i, oc := range st.entries {
				if oc.entry == old {
					st.entries[i] = c
					break
				}
			}
		} else {
			st.entries = append(st.entries, c)
		}
		st.byID[e.ID] = e
	}
}

// envExpand 展开 %NAME% 占位符；未知变量返回 false
func envExpand(pattern string) (string, bool) {
	out := pattern
	for {
		i := strings.Index(out, "%")
		if i < 0 {
			break
		}
		j := strings.Index(out[i+1:], "%")
		if j < 0 {
			return "", false
		}
		name := out[i+1 : i+1+j]
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			return "", false
		}
		out = out[:i] + v + out[i+1+j+1:]
	}
	return out, true
}

func normPath(p string) string {
	return strings.ToLower(strings.ReplaceAll(filepath.ToSlash(p), "/", "\\"))
}

func compile(e *Entry) *compiled {
	c := &compiled{entry: e}
	for _, pat := range e.Paths {
		pat = strings.TrimSuffix(strings.TrimSpace(pat), "/")
		if pat == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(pat, "**/"); ok {
			if exp, ok := envExpand(rest); ok {
				c.anywhere = append(c.anywhere, `\`+normPath(exp))
			}
			continue
		}
		if exp, ok := envExpand(pat); ok {
			c.expanded = append(c.expanded, normPath(exp))
		}
	}
	if len(c.expanded) == 0 && len(c.anywhere) == 0 {
		return nil
	}
	return c
}

// Match 给定一个扫描到的目录路径，返回命中的知识条目（按具体程度排序，最具体在前）。
func Match(path string) []Hit {
	st := Load()
	np := normPath(path)
	st.mu.RLock()
	defer st.mu.RUnlock()

	type scored struct {
		c     *compiled
		score int
		path  string
	}
	var hits []scored
	for _, c := range st.entries {
		best := 0
		bestPath := ""
		for _, exp := range c.expanded {
			if np == exp || strings.HasPrefix(np, exp+`\`) {
				if len(exp) > best {
					best, bestPath = len(exp), exp
				}
			}
		}
		for _, any := range c.anywhere {
			if np == strings.TrimPrefix(any, `\`) || strings.Contains(np, any) {
				// 任意位置命中：具体程度按尾部模式长度计，整体低于前缀命中
				if score := len(any) + 200; score > best && best < 200 {
					best, bestPath = score, strings.TrimPrefix(any, `\`)
				}
			}
		}
		if best > 0 {
			hits = append(hits, scored{c, best, bestPath})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, Hit{Entry: h.c.entry, MatchedPath: h.path, Exists: resolveExisting(h.c.entry)})
	}
	return out
}

// resolveExisting 广度连接：解析条目全部落盘点在本机的存在情况
func resolveExisting(e *Entry) []string {
	var exist []string
	seen := map[string]bool{}
	for _, pat := range e.Paths {
		p := strings.TrimSuffix(strings.TrimSpace(pat), "/")
		rest := p
		if _, ok := strings.CutPrefix(p, "**/"); ok {
			continue // 任意位置模式无法静态解析出具体路径
		}
		exp, ok := envExpand(rest)
		if !ok {
			continue
		}
		exp = filepath.FromSlash(exp)
		if !seen[strings.ToLower(exp)] {
			seen[strings.ToLower(exp)] = true
			if _, err := os.Stat(exp); err == nil {
				exist = append(exist, exp)
			}
		}
	}
	return exist
}

// RiskMark 返回风险等级的中文标记（CLI 用）
func RiskMark(risk string) string {
	switch risk {
	case RiskSafe:
		return "[安全]"
	case RiskCaution:
		return "[谨慎]"
	case RiskDanger:
		return "[危险]"
	case RiskData:
		return "[数据]"
	default:
		return "[说明]"
	}
}

var _ = fmt.Sprintf
