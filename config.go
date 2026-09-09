package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	WebhookURL                      string
	WebhookSecret                   string
	Keyword                         string
	SpecialWebhookURL               string
	SpecialWebhookSecret            string
	SpecialKeyword                  string
	CheckInterval                   time.Duration
	ReportInterval                  time.Duration
	CPUThresholdPercent             float64
	MemoryAvailableThresholdPercent float64
	DiskUsageThresholdPercent       float64
	ThermalAlertEnabled             bool
	TopProcessCount                 int
	HTTPTimeout                     time.Duration
}

func LoadConfig(path string) (Config, error) {
	fileValues, err := godotenv.Read(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}

	value := func(key, fallback string) string {
		if environmentValue, ok := os.LookupEnv(key); ok {
			return strings.TrimSpace(environmentValue)
		}
		if fileValue, ok := fileValues[key]; ok {
			return strings.TrimSpace(fileValue)
		}
		return fallback
	}

	config := Config{
		WebhookURL:           value("FEISHU_WEBHOOK_URL", ""),
		WebhookSecret:        value("FEISHU_WEBHOOK_SECRET", ""),
		Keyword:              value("FEISHU_KEYWORD", "Mac 负载监控"),
		SpecialWebhookURL:    value("SPECIAL_FEISHU_WEBHOOK_URL", ""),
		SpecialWebhookSecret: value("SPECIAL_FEISHU_WEBHOOK_SECRET", ""),
		SpecialKeyword:       value("SPECIAL_FEISHU_KEYWORD", "Mac 特别告警"),
	}
	if config.CheckInterval, err = parseDuration(value("CHECK_INTERVAL", "10m"), "CHECK_INTERVAL"); err != nil {
		return Config{}, err
	}
	if config.ReportInterval, err = parseDuration(value("REPORT_INTERVAL", "1h"), "REPORT_INTERVAL"); err != nil {
		return Config{}, err
	}
	if config.CPUThresholdPercent, err = parsePercent(value("CPU_THRESHOLD_PERCENT", "90"), "CPU_THRESHOLD_PERCENT"); err != nil {
		return Config{}, err
	}
	if config.MemoryAvailableThresholdPercent, err = parsePercent(value("MEMORY_AVAILABLE_THRESHOLD_PERCENT", "20"), "MEMORY_AVAILABLE_THRESHOLD_PERCENT"); err != nil {
		return Config{}, err
	}
	if config.DiskUsageThresholdPercent, err = parsePercent(value("DISK_USAGE_THRESHOLD_PERCENT", "90"), "DISK_USAGE_THRESHOLD_PERCENT"); err != nil {
		return Config{}, err
	}
	if config.ThermalAlertEnabled, err = strconv.ParseBool(value("THERMAL_ALERT_ENABLED", "true")); err != nil {
		return Config{}, fmt.Errorf("THERMAL_ALERT_ENABLED 必须是 true 或 false")
	}
	if config.TopProcessCount, err = strconv.Atoi(value("TOP_PROCESS_COUNT", "5")); err != nil || config.TopProcessCount < 0 || config.TopProcessCount > 20 {
		return Config{}, fmt.Errorf("TOP_PROCESS_COUNT 必须是 0 到 20 的整数")
	}
	if config.HTTPTimeout, err = parseDuration(value("HTTP_TIMEOUT", "10s"), "HTTP_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if config.Keyword == "" {
		return Config{}, fmt.Errorf("FEISHU_KEYWORD 不能为空")
	}
	if config.SpecialKeyword == "" {
		return Config{}, fmt.Errorf("SPECIAL_FEISHU_KEYWORD 不能为空")
	}
	return config, nil
}

func (config Config) Special() Config {
	special := config
	special.WebhookURL = config.SpecialWebhookURL
	special.WebhookSecret = config.SpecialWebhookSecret
	special.Keyword = config.SpecialKeyword
	return special
}

func (config Config) ValidateWebhook() error {
	return validateWebhookURL(config.WebhookURL, "FEISHU_WEBHOOK_URL")
}

func (config Config) ValidateSpecialWebhook() error {
	return validateWebhookURL(config.SpecialWebhookURL, "SPECIAL_FEISHU_WEBHOOK_URL")
}

func validateWebhookURL(raw, name string) error {
	if raw == "" {
		return fmt.Errorf("%s 不能为空", name)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "open.feishu.cn" || !strings.HasPrefix(parsed.Path, "/open-apis/bot/v2/hook/") {
		return fmt.Errorf("%s 必须是飞书自定义机器人的 HTTPS Webhook 地址", name)
	}
	return nil
}

func parseDuration(raw, name string) (time.Duration, error) {
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s 必须是大于 0 的 Go 时长，例如 10m 或 1h", name)
	}
	return duration, nil
}

func parsePercent(raw, name string) (float64, error) {
	percent, err := strconv.ParseFloat(raw, 64)
	if err != nil || percent < 0 || percent > 100 {
		return 0, fmt.Errorf("%s 必须是 0 到 100 之间的数字", name)
	}
	return percent, nil
}
