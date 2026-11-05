package recycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckLocks 锁规则全表：该锁的必须锁，该放的必须放
func TestCheckLocks(t *testing.T) {
	home, err := os.UserHomeDir() // C:\Users\<当前用户>
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		path    string
		locked  bool // reason != ""
		keyword string // reason 应包含的关键字（可空）
	}{
		{`C:\`, true, "盘根"},
		{`D:\`, true, "盘根"},
		{`C:\Windows`, true, "windows"},
		{`C:\Windows\System32\config`, true, "windows"},
		{`C:\Windows\Temp\某缓存`, true, "windows"},
		{`C:\Program Files\某游戏`, true, "program files"},
		{`C:\Program Files (x86)\某软件`, true, "program files"},
		{`C:\ProgramData\某程序`, true, "programdata"},
		{`C:\Boot`, true, "boot"},
		{`C:\Recovery`, true, "recovery"},
		{`C:\PerfLogs`, true, "perflogs"},
		{`C:\System Volume Information`, true, "system volume"},
		{`C:\$Recycle.Bin`, true, "$recycle.bin"},
		{`D:\$RECYCLE.BIN\S-1-5-18`, true, "$recycle.bin"},
		{`C:\pagefile.sys`, true, "页面"},
		{`C:\hiberfil.sys`, true, "页面"},
		{`C:\swapfile.sys`, true, "页面"},
		{`C:\Users`, true, "users"},
		{`C:\Users\Public`, true, "共享"},
		{`C:\Users\Default`, true, "共享"},
		{`C:\Users\Administrator`, true, "用户目录"},
		{`C:\Users\某个路人\文档`, true, "其他用户"},
		// 放行：当前用户 profile 全域（含 AppData——风险由知识库分级，不硬锁）
		{home, false, ""},
		{filepath.Join(home, `AppData`), false, ""},
		{filepath.Join(home, `AppData\Local`), false, ""},
		{filepath.Join(home, `Downloads`), false, ""},
		// 放行：普通目录
		{`C:\baidunetdiskdownload`, false, ""},
		{`D:\下载`, false, ""},
		{`C:\AI\test3`, false, ""},
	}
	for _, c := range cases {
		reason := Check(c.path)
		locked := reason != ""
		if locked != c.locked {
			t.Errorf("Check(%q) locked=%v reason=%q, want locked=%v", c.path, locked, reason, c.locked)
			continue
		}
		if c.locked && c.keyword != "" && !containsFold(reason, c.keyword) {
			t.Errorf("Check(%q) reason=%q 应包含 %q", c.path, reason, c.keyword)
		}
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// TestSendToRecycleReal 真实冒烟：自建临时文件送回收站，验证文件确实消失且被锁项被跳过
func TestSendToRecycleReal(t *testing.T) {
	tmp, err := os.MkdirTemp("", "disklens-rb-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmp) // 清理未删净的残留
	target := filepath.Join(tmp, "should-vanish.txt")
	if err := os.WriteFile(target, []byte("盘镜回收站冒烟测试"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// 混入一个锁定项，验证硬锁跳过
	res := SendToRecycle([]string{target, `C:\Windows`, filepath.Join(tmp, "不存在.txt")})
	if len(res) != 3 {
		t.Fatalf("结果数 %d != 3", len(res))
	}
	if !res[0].OK {
		t.Errorf("临时文件删除失败: %s", res[0].Reason)
	}
	if res[1].OK || !contains(res[1].Reason, "windows") {
		t.Errorf("C:\\Windows 应被硬锁拒绝，got ok=%v reason=%q", res[1].OK, res[1].Reason)
	}
	if res[2].OK || !contains(res[2].Reason, "不存在") {
		t.Errorf("不存在路径应被拒绝，got ok=%v reason=%q", res[2].OK, res[2].Reason)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("目标文件仍存在，回收站删除未生效")
	}
	t.Logf("冒烟通过：%s 已进回收站", target)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
