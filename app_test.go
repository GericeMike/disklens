package main

import (
	"encoding/json"
	"testing"
	"time"
)

// TestBindingFlow 端到端验证绑定层：扫描 → 概览 → 子目录 → 可清理汇总
func TestBindingFlow(t *testing.T) {
	app := NewApp()
	if err := app.StartScan([]string{"C:\\AI\\test3\\water-reminder"}); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	for app.IsScanning() {
		time.Sleep(200 * time.Millisecond)
	}
	ov := app.GetOverview()
	b, _ := json.Marshal(ov)
	t.Logf("GetOverview: %s", string(b))
	if !ov.Ready {
		t.Fatal("overview not ready")
	}
	if len(ov.Drives) == 0 {
		t.Fatal("GetOverview.Drives 为空 —— 这就是 GUI 卡片空白的根源之一")
	}

	kids := app.GetChildren("C:\\AI\\test3\\water-reminder")
	if len(kids) == 0 {
		t.Fatal("GetChildren 为空")
	}
	b, _ = json.Marshal(kids)
	t.Logf("GetChildren[0..2]: %s", trunc(string(b), 300))

	cleans := app.GetCleanSummary("C:")
	b, _ = json.Marshal(cleans)
	t.Logf("GetCleanSummary: %s", trunc(string(b), 300))

	ex := app.ExplainPath("C:\\AI\\test3\\water-reminder")
	b, _ = json.Marshal(ex)
	t.Logf("ExplainPath: %s", trunc(string(b), 300))
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
