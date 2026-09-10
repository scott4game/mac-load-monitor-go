package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaults(t *testing.T) {
	config, err := LoadConfig(writeTestEnv(t, "FEISHU_WEBHOOK_URL=\n"))
	if err != nil {
		t.Fatal(err)
	}
	if config.CheckInterval != 10*time.Minute || config.ReportInterval != time.Hour {
		t.Fatalf("unexpected intervals: %s, %s", config.CheckInterval, config.ReportInterval)
	}
	if config.ReportJitterMax != 30*time.Second {
		t.Fatalf("unexpected report jitter: %s", config.ReportJitterMax)
	}
	if config.CPUThresholdPercent != 90 || config.MemoryAvailableThresholdPercent != 20 || config.DiskUsageThresholdPercent != 90 {
		t.Fatalf("unexpected thresholds: %+v", config)
	}
	if config.SpecialKeyword != "Mac 特别告警" {
		t.Fatalf("unexpected special keyword: %s", config.SpecialKeyword)
	}
}

func TestLoadConfigEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("CPU_THRESHOLD_PERCENT", "75")
	config, err := LoadConfig(writeTestEnv(t, "CPU_THRESHOLD_PERCENT=90\n"))
	if err != nil {
		t.Fatal(err)
	}
	if config.CPUThresholdPercent != 75 {
		t.Fatalf("expected environment override, got %.1f", config.CPUThresholdPercent)
	}
}

func TestValidateWebhook(t *testing.T) {
	valid := Config{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test"}
	if err := valid.ValidateWebhook(); err != nil {
		t.Fatal(err)
	}
	invalid := Config{WebhookURL: "https://example.com/hook/secret"}
	if err := invalid.ValidateWebhook(); err == nil {
		t.Fatal("expected invalid webhook error")
	}
}

func TestSpecialConfigUsesSeparateEndpoint(t *testing.T) {
	config := Config{
		WebhookURL:           "https://open.feishu.cn/open-apis/bot/v2/hook/normal",
		WebhookSecret:        "normal-secret",
		Keyword:              "normal",
		SpecialWebhookURL:    "https://open.feishu.cn/open-apis/bot/v2/hook/special",
		SpecialWebhookSecret: "special-secret",
		SpecialKeyword:       "special",
	}
	special := config.Special()
	if special.WebhookURL != config.SpecialWebhookURL || special.WebhookSecret != config.SpecialWebhookSecret || special.Keyword != config.SpecialKeyword {
		t.Fatalf("unexpected special config: %+v", special)
	}
	if err := config.ValidateSpecialWebhook(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	_, err := LoadConfig(writeTestEnv(t, "CHECK_INTERVAL=0s\n"))
	if err == nil {
		t.Fatal("expected duration validation error")
	}
}

func TestLoadConfigAllowsZeroReportJitter(t *testing.T) {
	config, err := LoadConfig(writeTestEnv(t, "REPORT_JITTER_MAX=0s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if config.ReportJitterMax != 0 {
		t.Fatalf("expected disabled jitter, got %s", config.ReportJitterMax)
	}
}
