package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatMessageIncludesCompleteMetrics(t *testing.T) {
	config := monitorTestConfig()
	sample := Sample{
		At:                      time.Date(2026, 9, 9, 20, 0, 0, 0, time.FixedZone("SGT", 8*60*60)),
		Hostname:                "test-mac",
		CPUAvailable:            true,
		CPUPercent:              42.5,
		LogicalCPUs:             10,
		LoadAvailable:           true,
		Load1:                   2.1,
		Load5:                   1.8,
		Load15:                  1.5,
		MemoryCapacityAvailable: true,
		MemoryPressureAvailable: true,
		MemoryTotalBytes:        24 * bytesPerGiB,
		MemoryUsedBytes:         12 * bytesPerGiB,
		MemoryAvailableBytes:    12 * bytesPerGiB,
		MemoryUsedPercent:       50,
		MemoryAvailablePercent:  56,
		DiskAvailable:           true,
		DiskTotalBytes:          1000 * bytesPerGiB,
		DiskUsedBytes:           400 * bytesPerGiB,
		DiskFreeBytes:           600 * bytesPerGiB,
		DiskUsedPercent:         40,
		DiskRates:               DiskRates{Available: true, ReadBytesPerSecond: 2 * bytesPerMiB, WriteBytesPerSecond: bytesPerMiB, ReadIOPS: 10, WriteIOPS: 5},
		Thermal:                 ThermalStatus{Available: true, Summary: "正常"},
		Battery:                 BatteryStatus{Available: true, Percent: 80, Source: "AC Power", State: "not charging"},
		UptimeAvailable:         true,
		Uptime:                  25*time.Hour + 2*time.Minute,
	}
	message := FormatMessage(config, sample, "每小时状态", nil)
	for _, expected := range []string{"CPU: 42.5%", "负载均值", "压力可用评分 56.0%", "磁盘吞吐", "温度压力: 正常", "电池: 80%", "1天1小时2分钟"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message missing %q:\n%s", expected, message)
		}
	}
}
