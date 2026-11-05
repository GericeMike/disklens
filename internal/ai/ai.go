// Package ai AI 解释：OpenAI 兼容接口调用与配置管理。
// 零遥测原则：本包只在用户主动触发（解释抽屉点「AI 解释」/设置页点「测试连接」）时出网，
// 且仅访问 https 端点；API Key 经 Windows DPAPI 加密后落盘，不明文存储。
package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// unsafeSlice/unsafePtr DPAPI DataBlob ↔ Go 字节切片转换
func unsafeSlice[T any](p *T, n int) []byte { return unsafe.Slice((*byte)(unsafe.Pointer(p)), n) }

// Preset 提供商预设（OpenAI 兼容 chat/completions）
type Preset struct {
	Name         string `json:"name"`         // 显示名
	BaseURL      string `json:"baseUrl"`      // 接口根（不含 /chat/completions）
	DefaultModel string `json:"defaultModel"` // 默认模型
	KeyHint      string `json:"keyHint"`      // 获取 Key 的提示
}

// Presets 四预设；custom 的 baseUrl/model 由用户填写
var Presets = map[string]Preset{
	"zhipu":    {Name: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4", DefaultModel: "glm-4-flash", KeyHint: "open.bigmodel.cn 控制台获取 API Key"},
	"deepseek": {Name: "DeepSeek", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", KeyHint: "platform.deepseek.com 获取 API Key"},
	"kimi":     {Name: "Kimi 月之暗面", BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "moonshot-v1-8k", KeyHint: "platform.moonshot.cn 获取 API Key"},
	"custom":   {Name: "自定义 OpenAI 兼容", BaseURL: "", DefaultModel: "", KeyHint: "填兼容 OpenAI /chat/completions 的地址"},
}

// Config AI 配置（磁盘上 ApiKey 存的是 DPAPI 密文 base64，ConfigPlain 才含明文）
type Config struct {
	Provider string `json:"provider"` // zhipu / deepseek / kimi / custom
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl"` // custom 时生效
	ApiKey   string `json:"apiKey"`  // DPAPI 密文 base64；空=未配置
}

// configPath %APPDATA%\DiskLens\ai-config.json
func configPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return "ai-config.json"
	}
	return filepath.Join(base, "DiskLens", "ai-config.json")
}

// MaskKey 脱敏展示：只留前 4 位
func MaskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****"
}

// LoadConfig 读配置；文件不存在返回未配置状态
func LoadConfig() Config {
	var c Config
	data, err := os.ReadFile(configPath())
	if err != nil {
		return Config{Provider: "zhipu"}
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{Provider: "zhipu"}
	}
	if c.Provider == "" {
		c.Provider = "zhipu"
	}
	return c
}

// SaveConfig 写配置；plainKey 非空时加密替换（空串保留旧 key，"-" 清除）
func SaveConfig(provider, model, baseURL, plainKey string) (Config, error) {
	c := LoadConfig()
	oldProvider := c.Provider
	if provider != "" {
		if _, ok := Presets[provider]; !ok {
			return c, fmt.Errorf("未知提供商: %s", provider)
		}
		c.Provider = provider
	}
	if model != "" {
		c.Model = model
	}
	if baseURL != "" {
		c.BaseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	}
	if plainKey == "-" {
		c.ApiKey = ""
	} else if plainKey != "" {
		cipher, err := dpapiProtect(plainKey)
		if err != nil {
			return c, fmt.Errorf("加密存储失败: %w", err)
		}
		c.ApiKey = cipher
	}
	// custom 必须给 https 地址；预设 baseUrl 固定为官方端点，
	// 切换 provider 时模型重置为该家默认（避免别家地址/模型残留）
	p := Presets[c.Provider]
	if c.Provider == "custom" {
		if c.BaseURL == "" || !strings.HasPrefix(strings.ToLower(c.BaseURL), "https://") {
			return c, fmt.Errorf("自定义接口地址必须为 https:// 开头")
		}
	} else {
		c.BaseURL = p.BaseURL
		if c.Model == "" || oldProvider != c.Provider {
			c.Model = p.DefaultModel
		}
	}
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return c, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return c, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return c, err
	}
	return c, nil
}

// PlainKey 取明文 Key（仅调用上游接口时使用；无 Key 返回空串）
func PlainKey() string {
	c := LoadConfig()
	if c.ApiKey == "" {
		return ""
	}
	return dpapiUnprotect(c.ApiKey)
}

// Endpoint 实际请求地址
func Endpoint(c Config) string {
	base := c.BaseURL
	if base == "" {
		base = Presets[c.Provider].BaseURL
	}
	return strings.TrimSuffix(base, "/") + "/chat/completions"
}

// ChatMsg 消息
type ChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []ChatMsg `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 调用 OpenAI 兼容 chat/completions，返回回复文本。
// httpClient 可注入（测试用 mock server）；生产传 nil 用默认（30s 超时）。
func Chat(cfg Config, plainKey, system, user string, httpClient *http.Client) (string, error) {
	if plainKey == "" {
		return "", fmt.Errorf("未配置 API Key")
	}
	reqBody, err := json.Marshal(chatReq{
		Model: cfg.Model, Messages: []ChatMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0.3, MaxTokens: 600, // 200 字中文输出足够，约束模型不写长文
	})
	if err != nil {
		return "", err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	req, err := http.NewRequest(http.MethodPost, Endpoint(cfg), bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plainKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("网络请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	var cr chatResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("响应解析失败(HTTP %d): %.200s", resp.StatusCode, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if cr.Error != nil {
			msg = cr.Error.Message
		}
		if msg == "" {
			msg = string(body)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "API Key 无效或已过期（401）"
		}
		return "", fmt.Errorf("接口返回 %d: %s", resp.StatusCode, msg)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("接口未返回内容")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), nil
}

// ExplainPrompt 构造磁盘目录解读的系统提示词与用户内容。
// kbHits 为知识库命中的「名称：描述」列表（可为空）；样本目录/文件帮助模型判断内容性质。
func ExplainPrompt(path string, sizeBytes, fileCount int64, kbHits []string, sampleDirs, sampleFiles []string) (string, string) {
	system := `你是 Windows 磁盘清理助手，读者是不懂编程的普通用户。
风格：
1. 说人话：避免行话；必须用术语时，紧跟一句大白话解释或生活化比方（如"缓存=快递中转仓，下载一次各项目共用"）；
2. 克制：全文 200 字以内，宁可少说，不事无巨细；
3. 固定三段，每段一两句：
【这是什么】1 句，带类比，让小白秒懂
【能否删除】结论（可删/谨慎/别删）+ 后果 1 句
【建议】最多 2 条，给可直接执行的命令或动作
4. 信息不足就写"无法确定"，不编造；不复述任何原始数据。`

	var b strings.Builder
	b.WriteString("请解读这个目录：\n\n")
	fmt.Fprintf(&b, "路径：%s\n", path)
	b.WriteString(fmt.Sprintf("总占用：%s，文件数：%d\n", HumanBytes(sizeBytes), fileCount))
	if len(sampleDirs) > 0 {
		b.WriteString("子目录：" + strings.Join(sampleDirs, "、") + "\n")
	}
	if len(sampleFiles) > 0 {
		b.WriteString("文件示例：" + strings.Join(sampleFiles, "、") + "\n")
	}
	if len(kbHits) > 0 {
		b.WriteString("本地知识库参考：\n")
		for _, h := range kbHits {
			b.WriteString("- " + h + "\n")
		}
	} else {
		b.WriteString("本地知识库：无该目录条目\n")
	}
	return system, b.String()
}

// HumanBytes 字节量中文可读格式（与主程序 humanBytes 口径一致）
func HumanBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := int64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(v)/float64(div), "KMGTPE"[exp])
}

// ---------- Windows DPAPI（CurrentUser 范围加密，重启/其他账户不可解） ----------

// dpapiProtect 明文 → DPAPI 密文 base64
func dpapiProtect(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	enc := utf16.Encode([]rune(plain))
	blob := windows.DataBlob{Size: uint32(len(enc) * 2), Data: (*byte)(unsafe.Pointer(&enc[0]))}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&blob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	raw := unsafeSlice(out.Data, int(out.Size))
	return base64.StdEncoding.EncodeToString(raw), nil
}

// dpapiUnprotect DPAPI 密文 base64 → 明文
func dpapiUnprotect(cipher string) string {
	if cipher == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(cipher)
	if err != nil || len(raw) == 0 {
		return ""
	}
	buf := make([]byte, len(raw))
	copy(buf, raw)
	blob := windows.DataBlob{Size: uint32(len(buf)), Data: &buf[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&blob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return ""
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	data := unsafeSlice(out.Data, int(out.Size))
	// 密文是 UTF-16LE 编码的原文
	u16 := make([]uint16, out.Size/2)
	for i := range u16 {
		u16[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	return string(utf16.Decode(u16))
}
