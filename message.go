package main

import (
	"fmt"
	"strings"
	"time"
)

const bytesPerGiB = 1024 * 1024 * 1024
const bytesPerMiB = 1024 * 1024

func FormatMessage(config Config, sample Sample, status string, breaches []Breach) string {
	lines := []string{
		fmt.Sprintf("%s | %s", config.Keyword, status),
		fmt.Sprintf("主机: %s", fallback(sample.Hostname, "未知")),
		fmt.Sprintf("时间: %s", sample.At.Format("2006-01-02 15:04:05 -0700")),
	}
	if sample.CPUAvailable {
		lines = append(lines, fmt.Sprintf("CPU: %.1f%%（告警线 %.1f%%）", sample.CPUPercent, config.CPUThresholdPercent))
	} else {
		lines = append(lines, "CPU: 不可用")
	}
	if sample.LoadAvailable {
		lines = append(lines, fmt.Sprintf("负载均值: %.2f / %.2f / %.2f（%d 逻辑核心）", sample.Load1, sample.Load5, sample.Load15, sample.LogicalCPUs))
	} else {
		lines = append(lines, "负载均值: 不可用")
	}
	if sample.MemoryCapacityAvailable && sample.MemoryPressureAvailable {
		lines = append(lines, fmt.Sprintf(
			"内存: 已用 %.1f / %.1f GiB（%.1f%%），可用 %.1f GiB，压力可用评分 %.1f%%（告警线 %.1f%%）",
			float64(sample.MemoryUsedBytes)/bytesPerGiB,
			float64(sample.MemoryTotalBytes)/bytesPerGiB,
			sample.MemoryUsedPercent,
			float64(sample.MemoryAvailableBytes)/bytesPerGiB,
			sample.MemoryAvailablePercent,
			config.MemoryAvailableThresholdPercent,
		))
	} else if sample.MemoryCapacityAvailable {
		lines = append(lines, fmt.Sprintf(
			"内存: 已用 %.1f / %.1f GiB（%.1f%%），可用 %.1f GiB，压力可用评分不可用",
			float64(sample.MemoryUsedBytes)/bytesPerGiB,
			float64(sample.MemoryTotalBytes)/bytesPerGiB,
			sample.MemoryUsedPercent,
			float64(sample.MemoryAvailableBytes)/bytesPerGiB,
		))
	} else if sample.MemoryPressureAvailable {
		lines = append(lines, fmt.Sprintf(
			"内存容量: 不可用，压力可用评分 %.1f%%（告警线 %.1f%%）",
			sample.MemoryAvailablePercent,
			config.MemoryAvailableThresholdPercent,
		))
	} else {
		lines = append(lines, "内存: 不可用")
	}
	if sample.DiskAvailable {
		lines = append(lines, fmt.Sprintf(
			"系统盘 /: 已用 %.1f / %.1f GiB（%.1f%%），可用 %.1f GiB（告警线 %.1f%%）",
			float64(sample.DiskUsedBytes)/bytesPerGiB,
			float64(sample.DiskTotalBytes)/bytesPerGiB,
			sample.DiskUsedPercent,
			float64(sample.DiskFreeBytes)/bytesPerGiB,
			config.DiskUsageThresholdPercent,
		))
	} else {
		lines = append(lines, "系统盘容量: 不可用")
	}
	if sample.DiskRates.Available {
		lines = append(lines, fmt.Sprintf(
			"磁盘吞吐: 读 %.2f MB/s（%.1f IOPS），写 %.2f MB/s（%.1f IOPS）",
			sample.DiskRates.ReadBytesPerSecond/bytesPerMiB,
			sample.DiskRates.ReadIOPS,
			sample.DiskRates.WriteBytesPerSecond/bytesPerMiB,
			sample.DiskRates.WriteIOPS,
		))
	} else {
		lines = append(lines, "磁盘吞吐: 暂无区间数据")
	}
	if sample.Thermal.Available {
		lines = append(lines, "温度压力: "+sample.Thermal.Summary)
	} else {
		lines = append(lines, "温度压力: 不可用")
	}
	if sample.Battery.Available {
		battery := fmt.Sprintf("电池: %.0f%%，%s", sample.Battery.Percent, sample.Battery.Source)
		if sample.Battery.State != "" {
			battery += "，" + sample.Battery.State
		}
		lines = append(lines, battery)
	} else {
		lines = append(lines, "电池: 不可用")
	}
	if sample.UptimeAvailable {
		lines = append(lines, "运行时长: "+formatDuration(sample.Uptime))
	}
	if len(breaches) > 0 {
		lines = append(lines, "过载项目:")
		for _, breach := range breaches {
			lines = append(lines, fmt.Sprintf("  %s: %s", breach.Metric, breach.Detail))
		}
	}
	lines = append(lines, formatProcesses("CPU 占用最高进程:", sample.TopCPU, true)...)
	lines = append(lines, formatProcesses("内存占用最高进程:", sample.TopMemory, false)...)
	if len(sample.Warnings) > 0 {
		lines = append(lines, "采集提示: "+strings.Join(uniqueStrings(sample.Warnings), "、"))
	}
	return strings.Join(lines, "\n")
}

func formatProcesses(title string, processes []ProcessUsage, cpuMetric bool) []string {
	lines := []string{title}
	if len(processes) == 0 {
		return append(lines, "  无可用数据")
	}
	for _, process := range processes {
		value := fmt.Sprintf("%.1f%%", process.CPUPercent)
		if !cpuMetric {
			value = fmt.Sprintf("%.1f MiB", float64(process.MemoryBytes)/bytesPerMiB)
		}
		lines = append(lines, fmt.Sprintf("  %d  %s  %s", process.PID, process.Name, value))
	}
	return lines
}

func formatDuration(duration time.Duration) string {
	totalMinutes := int(duration.Round(time.Minute).Minutes())
	days := totalMinutes / (24 * 60)
	hours := totalMinutes % (24 * 60) / 60
	minutes := totalMinutes % 60
	if days > 0 {
		return fmt.Sprintf("%d天%d小时%d分钟", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时%d分钟", hours, minutes)
	}
	return fmt.Sprintf("%d分钟", minutes)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
