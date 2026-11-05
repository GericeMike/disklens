// Package recycle — 删除一律送回收站（SHFileOperationW + FOF_ALLOWUNDO）。
// 硬性安全边界（不可绕过）：
//   1. 仅本地固定磁盘（可移动/网络盘送回收站不可靠，可能变成永久删除，一律拒绝）；
//   2. 系统路径硬锁：盘根、Windows、Program Files(含 x86)、ProgramData、Boot、
//      Recovery、PerfLogs、System Volume Information、$Recycle.Bin（任意盘）、
//      pagefile/hiberfil/swapfile、Users 下除当前用户与已知共享目录外的其他用户；
//   3. 路径必须存在。
// 校验失败绝不调用 shell 删除。
package recycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Result 单项删除结果
type Result struct {
	Path   string `json:"path"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// 锁定的顶层目录名（相对盘根，小写比较）
var lockedTopDirs = map[string]bool{
	"windows":                     true,
	"program files":               true,
	"program files (x86)":         true,
	"programdata":                 true,
	"boot":                        true,
	"recovery":                    true,
	"perflogs":                    true,
	"system volume information":   true,
	"$recycle.bin":                true,
	"users":                       true, // Users 本身锁定；其下仅放行当前用户 profile 与下方白名单
}

// Users 下放行的共享/系统目录之外，其余用户一律锁定
var lockedUserDirs = map[string]bool{
	"public":          true,
	"default":         true,
	"administrator":   true, // 内置管理员目录即使存在也锁定
}

// 系统页面/休眠文件（任意位置按名锁定）
var lockedSysFiles = map[string]bool{
	"pagefile.sys": true,
	"hiberfil.sys": true,
	"swapfile.sys": true,
}

// expandLongPath 把 8.3 短名（如 MURPHY~1）展开为长路径。
// GetLongPathName 要求路径组件存在；末段不存在时逐级回退，展开最长存在祖先再拼回尾段。
func expandLongPath(p string) string {
	p = filepath.Clean(p)
	if out := tryLong(p); out != "" {
		return out
	}
	var tails []string
	cur := p
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tails = append([]string{filepath.Base(cur)}, tails...)
		cur = parent
		if out := tryLong(cur); out != "" {
			for _, t := range tails {
				out = filepath.Join(out, t)
			}
			return out
		}
	}
	return p
}

func tryLong(p string) string {
	u16, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	n, _ := windows.GetLongPathName(u16, nil, 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	n, _ = windows.GetLongPathName(u16, &buf[0], n)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// Check 返回锁定原因；空串表示允许删除
func Check(path string) string {
	p := filepath.Clean(strings.TrimSpace(path))
	if p == "" {
		return "路径为空"
	}
	p = expandLongPath(p)
	vol := filepath.VolumeName(p)
	if vol == "" {
		return "无法识别所在磁盘"
	}
	if windows.GetDriveType(windows.StringToUTF16Ptr(vol+`\`)) != windows.DRIVE_FIXED {
		return "仅支持本地固定磁盘送回收站（避免可移动盘永久删除）"
	}
	lower := strings.ToLower(filepath.Clean(p))
	// 盘根本身
	if lower == strings.ToLower(vol)+`\` || lower == strings.ToLower(vol) {
		return "不能删除盘根"
	}
	base := strings.ToLower(filepath.Base(p))
	if lockedSysFiles[base] {
		return "系统页面/休眠文件受保护"
	}
	rel := strings.TrimPrefix(lower, strings.ToLower(vol)+`\`)
	rel = strings.TrimSuffix(rel, `\`)
	// 顶层目录锁（任意盘的 SVI/$Recycle.Bin 都锁，其余仅系统盘语义一致，索性任意盘都锁）
	top := strings.ToLower(rel)
	if i := strings.Index(rel, `\`); i >= 0 {
		top = strings.ToLower(rel[:i])
	}
	if lockedTopDirs[top] {
		// Users 例外：放行当前用户 profile（落穿到末尾的存在性检查，不得提前 return）
		if top == "users" {
			allowed := false
			if home, err := os.UserHomeDir(); err == nil {
				home = expandLongPath(filepath.Clean(home))
				hl := strings.ToLower(filepath.Clean(home)) // C:\Users\xxx
				if lower == hl || strings.HasPrefix(lower, hl+`\`) {
					allowed = true // 自己的 profile 内允许（AppData 不锁，风险由知识库分级展示）
				}
			}
			if !allowed {
				second := rel
				if i := strings.Index(rel, `\`); i >= 0 {
					second = rel[i+1:]
				} else {
					return "Users 目录受保护"
				}
				u := strings.ToLower(second)
				if j := strings.Index(u, `\`); j >= 0 {
					u = u[:j]
				}
				if lockedUserDirs[u] {
					return "共享/系统用户目录受保护"
				}
				return "其他用户目录受保护"
			}
		} else {
			return "系统目录受保护: " + top
		}
	}
	// 存在性检查放在锁定判定之后：锁定项无论存在与否都报锁定原因（不泄露存在性）
	if _, err := os.Stat(p); err != nil {
		return "路径不存在或无法访问"
	}
	return ""
}

// SendToRecycle 批量送回收站。逐项校验，被锁项跳过并返回原因；结果保持输入顺序。
func SendToRecycle(paths []string) []Result {
	out := make([]Result, len(paths))
	var pending []string
	var pendingIdx []int
	for i, p := range paths {
		if reason := Check(p); reason != "" {
			out[i] = Result{Path: p, OK: false, Reason: reason}
			continue
		}
		cp := filepath.Clean(strings.TrimSpace(p))
		pending = append(pending, cp)
		pendingIdx = append(pendingIdx, i)
	}
	if len(pending) > 0 {
		err := shellDelete(pending)
		for j, i := range pendingIdx {
			if err != nil {
				out[i] = Result{Path: pending[j], OK: false, Reason: err.Error()}
			} else {
				out[i] = Result{Path: pending[j], OK: true}
			}
		}
	}
	return out
}

const (
	foDelete        = 3
	fofAllowUndo    = 0x40
	fofNoConfirmation = 0x10
	fofSilent       = 0x4
	fofNoErrorUI    = 0x400
)

type shFileOpStructW struct {
	hwnd              uintptr
	wFunc             uint32
	pFrom             *uint16
	pTo               *uint16
	fFlags            uint16
	fAnyOperationsAb  int32
	hNameMappings     uintptr
	lpszProgressTitle uintptr
}

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
)

// shellDelete 调 SHFileOperationW 把若干路径送回收站（FOF_ALLOWUNDO）
func shellDelete(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// pFrom 要求双 NUL 结尾的路径列表（每项自带一个 NUL，列表再补一个终止 NUL）
	var w []uint16
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p)
		if err != nil {
			return fmt.Errorf("路径含非法字符: %s", p)
		}
		w = append(w, u...)
	}
	w = append(w, 0)
	op := shFileOpStructW{
		wFunc:  foDelete,
		pFrom:  &w[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return fmt.Errorf("系统删除失败（代码 0x%x）", ret)
	}
	if op.fAnyOperationsAb != 0 {
		return fmt.Errorf("操作被系统中止")
	}
	return nil
}
