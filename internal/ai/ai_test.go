package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMaskKey 脱敏：只留前 4 位
func TestMaskKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "****"},
		{"abcd", "****"},
		{"abcdefg", "abcd****"},
		{"sk-1234567890", "sk-1****"},
	}
	for _, c := range cases {
		if got := MaskKey(c.in); got != c.want {
			t.Errorf("MaskKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDPAPIRoundtrip DPAPI 加密往返（CurrentUser 内可解；明文不落盘）
func TestDPAPIRoundtrip(t *testing.T) {
	const secret = "sk-test-abcdef123456"
	cipher, err := dpapiProtect(secret)
	if err != nil {
		t.Fatalf("protect: %v", err)
	}
	if strings.Contains(cipher, secret) {
		t.Fatal("密文中不得包含明文")
	}
	if got := dpapiUnprotect(cipher); got != secret {
		t.Fatalf("unprotect = %q, want %q", got, secret)
	}
	if dpapiUnprotect("not-base64!!!") != "" {
		t.Fatal("坏密文应返回空串")
	}
}

// TestPresets 四预设齐备且 baseUrl 均 https
func TestPresets(t *testing.T) {
	for id, p := range Presets {
		if p.Name == "" {
			t.Errorf("%s 无显示名", id)
		}
		if id != "custom" && !strings.HasPrefix(p.BaseURL, "https://") {
			t.Errorf("%s baseUrl 非 https: %s", id, p.BaseURL)
		}
	}
}

// TestEndpoint 拼接 /chat/completions 且去尾斜杠
func TestEndpoint(t *testing.T) {
	c := Config{Provider: "custom", BaseURL: "https://x.example.com/v1/"}
	if got := Endpoint(c); got != "https://x.example.com/v1/chat/completions" {
		t.Errorf("Endpoint = %s", got)
	}
}

// TestSaveConfigCustomRequiresHTTPS 自定义地址必须 https
func TestSaveConfigCustomRequiresHTTPS(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 配置写到临时目录，不碰真实 %APPDATA%
	if _, err := SaveConfig("custom", "m", "http://insecure.example.com", "k"); err == nil {
		t.Fatal("http 地址应被拒绝")
	}
	c, err := SaveConfig("custom", "my-model", "https://ok.example.com/v1", "sk-secret")
	if err != nil {
		t.Fatalf("https 保存失败: %v", err)
	}
	if c.Model != "my-model" || c.BaseURL != "https://ok.example.com/v1" {
		t.Fatalf("配置回读不符: %+v", c)
	}
	// 密文不含明文 key
	if c.ApiKey == "" || strings.Contains(c.ApiKey, "sk-secret") {
		t.Fatal("apiKey 应为 DPAPI 密文")
	}
	// 明文可解回
	if PlainKey() != "sk-secret" {
		t.Fatalf("PlainKey 解密不符: %q", PlainKey())
	}
	// 空 key 保存 = 保留旧 key
	if _, err := SaveConfig("custom", "m2", "", ""); err != nil {
		t.Fatalf("空 key 保存失败: %v", err)
	}
	if PlainKey() != "sk-secret" {
		t.Fatal("空 key 保存不应清掉旧 key")
	}
	// "-" 清除
	SaveConfig("custom", "m2", "", "-")
	if PlainKey() != "" {
		t.Fatal("'-' 应清除 key")
	}
	// 预设回填默认 model
	c2, err := SaveConfig("deepseek", "", "", "")
	if err != nil {
		t.Fatalf("deepseek 保存失败: %v", err)
	}
	if c2.Model != Presets["deepseek"].DefaultModel || c2.BaseURL != Presets["deepseek"].BaseURL {
		t.Fatalf("预设回填不符: %+v", c2)
	}
}

// TestChatMock httptest mock server 验证请求头/响应解析/错误分支
func TestChatMock(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("路径应为 /chat/completions，实际 %s", r.URL.Path)
		}
		var req chatReq
		json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			t.Errorf("消息结构不符: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"【这是什么】测试回复"}}]}`))
	}))
	defer srv.Close()

	cfg := Config{Provider: "custom", BaseURL: srv.URL, Model: "test-model"}
	out, err := Chat(cfg, "sk-mock", "system-prompt", "user-ask", srv.Client())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer sk-mock" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q", gotModel)
	}
	if !strings.Contains(out, "测试回复") {
		t.Errorf("回复不符: %q", out)
	}

	// 401 分支：明确提示 Key 无效
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv401.Close()
	cfg401 := Config{Provider: "custom", BaseURL: srv401.URL, Model: "m"}
	_, err = Chat(cfg401, "sk-bad", "s", "u", srv401.Client())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 应返回含 401 的错误: %v", err)
	}

	// 无 key 快速失败
	if _, err := Chat(cfg, "", "s", "u", nil); err == nil {
		t.Fatal("无 key 应报错")
	}
}

// TestExplainPrompt 提示词包含关键上下文与风格约束
func TestExplainPrompt(t *testing.T) {
	system, user := ExplainPrompt(`C:\demo`, 5<<30, 123,
		[]string{"缓存目录（谨慎）：存放更新包"},
		[]string{"cache(1.2 GB)", "logs(20 MB)"},
		[]string{"setup.exe", "readme.txt"},
	)
	// 面向小白的讲人话要求 + 字数克制 + 三段结构
	for _, want := range []string{"说人话", "大白话", "200 字", "能否删除", "编造"} {
		if !strings.Contains(system, want) {
			t.Errorf("系统提示词缺少 %q", want)
		}
	}
	for _, want := range []string{`C:\demo`, "5.0 GB", "123", "cache(1.2 GB)", "setup.exe", "缓存目录"} {
		if !strings.Contains(user, want) {
			t.Errorf("用户提示词缺少 %q", want)
		}
	}
	// 无知识库命中也要有占位说明
	_, user2 := ExplainPrompt(`C:\x`, 1, 1, nil, nil, nil)
	if !strings.Contains(user2, "无该目录条目") {
		t.Errorf("无命中时应说明知识库无条目")
	}
}

// TestHumanBytes 字节量格式
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"}, {512, "512 B"}, {1 << 10, "1.0 KB"}, {5 << 30, "5.0 GB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.in); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
