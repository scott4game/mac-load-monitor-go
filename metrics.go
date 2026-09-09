package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

var (
	memoryPressurePattern = regexp.MustCompile(`System-wide memory free percentage:\s*(\d+(?:\.\d+)?)%`)
	wholeDiskPattern      = regexp.MustCompile(`^disk\d+$`)
	batteryPercentPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	batterySourcePattern  = regexp.MustCompile(`Now drawing from '([^']+)'`)
	thermalLimitPattern   = regexp.MustCompile(`(?i)(CPU_Speed_Limit|Scheduler_Limit)\s*=\s*(\d+)`)
	warningLevelPattern   = regexp.MustCompile(`(?i)(thermal warning level|performance warning level)[^\d]*(\d+)`)
)

type SystemCollector struct {
	previousDisk *DiskSnapshot
	now          func() time.Time
	runCommand   func(context.Context, string, ...string) ([]byte, error)
}

func NewSystemCollector() *SystemCollector {
	return &SystemCollector{
		now: time.Now,
		runCommand: func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, arguments...).Output()
		},
	}
}

func (collector *SystemCollector) ResetDiskBaseline() {
	collector.previousDisk = nil
}

func (collector *SystemCollector) Collect(ctx context.Context) Sample {
	sample := Sample{At: collector.now().Local()}
	if hostname, err := host.InfoWithContext(ctx); err == nil {
		sample.Hostname = hostname.Hostname
		sample.Uptime = time.Duration(hostname.Uptime) * time.Second
		sample.UptimeAvailable = true
	} else {
		sample.Warnings = append(sample.Warnings, "主机信息不可用")
	}

	initialDisk, diskErr := readDiskSnapshot(ctx, collector.now)
	if diskErr != nil {
		sample.Warnings = append(sample.Warnings, "磁盘吞吐不可用")
	}

	if counts, err := cpu.CountsWithContext(ctx, true); err == nil {
		sample.LogicalCPUs = counts
	} else {
		sample.Warnings = append(sample.Warnings, "CPU 核心数不可用")
	}
	if percentages, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(percentages) == 1 {
		sample.CPUPercent = percentages[0]
		sample.CPUAvailable = true
	} else {
		sample.Warnings = append(sample.Warnings, "CPU 使用率不可用")
	}
	if average, err := load.AvgWithContext(ctx); err == nil {
		sample.Load1 = average.Load1
		sample.Load5 = average.Load5
		sample.Load15 = average.Load15
		sample.LoadAvailable = true
	} else {
		sample.Warnings = append(sample.Warnings, "负载均值不可用")
	}
	if memory, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		sample.MemoryTotalBytes = memory.Total
		sample.MemoryUsedBytes = memory.Used
		sample.MemoryAvailableBytes = memory.Available
		sample.MemoryUsedPercent = memory.UsedPercent
		sample.MemoryCapacityAvailable = true
	} else {
		sample.Warnings = append(sample.Warnings, "内存容量不可用")
	}
	if output, err := collector.runCommand(ctx, "/usr/bin/memory_pressure", "-Q"); err == nil {
		if available, parseErr := parseMemoryPressure(string(output)); parseErr == nil {
			sample.MemoryAvailablePercent = available
			sample.MemoryPressureAvailable = true
		} else {
			sample.Warnings = append(sample.Warnings, "内存压力不可用")
		}
	} else {
		sample.Warnings = append(sample.Warnings, "内存压力不可用")
	}
	if usage, err := disk.UsageWithContext(ctx, "/"); err == nil {
		sample.DiskTotalBytes = usage.Total
		sample.DiskUsedBytes = usage.Used
		sample.DiskFreeBytes = usage.Free
		sample.DiskUsedPercent = usage.UsedPercent
		sample.DiskAvailable = true
	} else {
		sample.Warnings = append(sample.Warnings, "系统盘容量不可用")
	}
	if output, err := collector.runCommand(ctx, "/usr/bin/pmset", "-g", "therm"); err == nil {
		sample.Thermal = parseThermalStatus(string(output), sample.LogicalCPUs)
	} else {
		sample.Thermal = ThermalStatus{Summary: "不可用"}
		sample.Warnings = append(sample.Warnings, "温度压力不可用")
	}
	if output, err := collector.runCommand(ctx, "/usr/bin/pmset", "-g", "batt"); err == nil {
		sample.Battery = parseBatteryStatus(string(output))
	} else {
		sample.Battery = BatteryStatus{State: "不可用"}
		sample.Warnings = append(sample.Warnings, "电池状态不可用")
	}

	if diskErr == nil {
		currentDisk := initialDisk
		if collector.previousDisk == nil {
			if secondDisk, err := readDiskSnapshot(ctx, collector.now); err == nil {
				currentDisk = secondDisk
				sample.DiskRates = calculateDiskRates(initialDisk, secondDisk)
			}
		} else {
			sample.DiskRates = calculateDiskRates(*collector.previousDisk, initialDisk)
		}
		collector.previousDisk = &currentDisk
	}
	return sample
}

func (collector *SystemCollector) AddTopProcesses(ctx context.Context, sample *Sample, count int) {
	if count <= 0 {
		return
	}
	output, err := collector.runCommand(ctx, "/bin/ps", "-A", "-o", "pid=,pcpu=,rss=,comm=")
	if err != nil {
		sample.Warnings = append(sample.Warnings, "进程排行不可用")
		return
	}
	processes := parseProcesses(string(output))
	sort.Slice(processes, func(left, right int) bool {
		return processes[left].CPUPercent > processes[right].CPUPercent
	})
	sample.TopCPU = append([]ProcessUsage(nil), processes[:min(count, len(processes))]...)
	sort.Slice(processes, func(left, right int) bool {
		return processes[left].MemoryBytes > processes[right].MemoryBytes
	})
	sample.TopMemory = append([]ProcessUsage(nil), processes[:min(count, len(processes))]...)
}

func readDiskSnapshot(ctx context.Context, now func() time.Time) (DiskSnapshot, error) {
	counters, err := disk.IOCountersWithContext(ctx)
	if err != nil {
		return DiskSnapshot{}, err
	}
	if len(counters) == 0 {
		return DiskSnapshot{}, fmt.Errorf("没有磁盘计数器")
	}
	selected := make([]disk.IOCountersStat, 0, len(counters))
	for name, counter := range counters {
		if wholeDiskPattern.MatchString(name) {
			selected = append(selected, counter)
		}
	}
	if len(selected) == 0 {
		for _, counter := range counters {
			selected = append(selected, counter)
		}
	}
	snapshot := DiskSnapshot{At: now()}
	for _, counter := range selected {
		snapshot.ReadBytes += counter.ReadBytes
		snapshot.WriteBytes += counter.WriteBytes
		snapshot.ReadCount += counter.ReadCount
		snapshot.WriteCount += counter.WriteCount
	}
	return snapshot, nil
}

func calculateDiskRates(previous, current DiskSnapshot) DiskRates {
	seconds := current.At.Sub(previous.At).Seconds()
	if seconds <= 0 || current.ReadBytes < previous.ReadBytes || current.WriteBytes < previous.WriteBytes || current.ReadCount < previous.ReadCount || current.WriteCount < previous.WriteCount {
		return DiskRates{}
	}
	return DiskRates{
		ReadBytesPerSecond:  float64(current.ReadBytes-previous.ReadBytes) / seconds,
		WriteBytesPerSecond: float64(current.WriteBytes-previous.WriteBytes) / seconds,
		ReadIOPS:            float64(current.ReadCount-previous.ReadCount) / seconds,
		WriteIOPS:           float64(current.WriteCount-previous.WriteCount) / seconds,
		Available:           true,
	}
}

func parseMemoryPressure(output string) (float64, error) {
	match := memoryPressurePattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return 0, fmt.Errorf("无法解析 memory_pressure 输出")
	}
	return strconv.ParseFloat(match[1], 64)
}

func parseThermalStatus(output string, logicalCPUs int) ThermalStatus {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ThermalStatus{Summary: "不可用"}
	}
	alert := false
	details := make([]string, 0, 2)
	for _, match := range thermalLimitPattern.FindAllStringSubmatch(output, -1) {
		limit, _ := strconv.Atoi(match[2])
		if limit < 100 {
			alert = true
			details = append(details, fmt.Sprintf("%s=%d%%", match[1], limit))
		}
	}
	for _, match := range warningLevelPattern.FindAllStringSubmatch(output, -1) {
		level, _ := strconv.Atoi(match[2])
		lineStart := strings.ToLower(match[0])
		if level > 0 && !strings.Contains(lineStart, "no ") {
			alert = true
			details = append(details, fmt.Sprintf("%s=%d", match[1], level))
		}
	}
	availableCPUPattern := regexp.MustCompile(`(?i)Available_CPUs\s*=\s*(\d+)`)
	if match := availableCPUPattern.FindStringSubmatch(output); len(match) == 2 && logicalCPUs > 0 {
		available, _ := strconv.Atoi(match[1])
		if available < logicalCPUs {
			alert = true
			details = append(details, fmt.Sprintf("Available_CPUs=%d/%d", available, logicalCPUs))
		}
	}
	if alert {
		return ThermalStatus{Available: true, Alert: true, Summary: strings.Join(details, ", ")}
	}
	return ThermalStatus{Available: true, Summary: "正常"}
}

func parseBatteryStatus(output string) BatteryStatus {
	percentMatch := batteryPercentPattern.FindStringSubmatch(output)
	if len(percentMatch) != 2 {
		return BatteryStatus{State: "不可用"}
	}
	percent, err := strconv.ParseFloat(percentMatch[1], 64)
	if err != nil {
		return BatteryStatus{State: "不可用"}
	}
	source := "未知电源"
	if sourceMatch := batterySourcePattern.FindStringSubmatch(output); len(sourceMatch) == 2 {
		source = sourceMatch[1]
	}
	state := ""
	lines := strings.Split(output, "\n")
	if len(lines) > 1 {
		fields := strings.Split(strings.TrimSpace(lines[1]), ";")
		if len(fields) >= 3 {
			state = strings.TrimSpace(fields[2])
		} else if len(fields) >= 2 {
			state = strings.TrimSpace(fields[1])
		}
	}
	return BatteryStatus{Available: true, Percent: percent, Source: source, State: state}
}

func parseProcesses(output string) []ProcessUsage {
	processes := make([]ProcessUsage, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		cpuPercent, cpuErr := strconv.ParseFloat(fields[1], 64)
		residentKB, memoryErr := strconv.ParseUint(fields[2], 10, 64)
		if pidErr != nil || cpuErr != nil || memoryErr != nil {
			continue
		}
		commandStart := strings.Index(line, fields[3])
		command := fields[3]
		if commandStart >= 0 {
			command = strings.TrimSpace(line[commandStart:])
		}
		processes = append(processes, ProcessUsage{
			PID:         pid,
			CPUPercent:  cpuPercent,
			MemoryBytes: residentKB * 1024,
			Name:        filepath.Base(command),
		})
	}
	return processes
}

func EvaluateBreaches(config Config, sample Sample) []Breach {
	breaches := make([]Breach, 0, 4)
	if sample.CPUAvailable && sample.CPUPercent >= config.CPUThresholdPercent {
		breaches = append(breaches, Breach{"CPU", fmt.Sprintf("%.1f%% >= %.1f%%", sample.CPUPercent, config.CPUThresholdPercent)})
	}
	if sample.MemoryPressureAvailable && sample.MemoryAvailablePercent < config.MemoryAvailableThresholdPercent {
		breaches = append(breaches, Breach{"内存", fmt.Sprintf("可用评分 %.1f%% < %.1f%%", sample.MemoryAvailablePercent, config.MemoryAvailableThresholdPercent)})
	}
	if sample.DiskAvailable && sample.DiskUsedPercent >= config.DiskUsageThresholdPercent {
		breaches = append(breaches, Breach{"磁盘容量", fmt.Sprintf("%.1f%% >= %.1f%%", sample.DiskUsedPercent, config.DiskUsageThresholdPercent)})
	}
	if config.ThermalAlertEnabled && sample.Thermal.Available && sample.Thermal.Alert {
		breaches = append(breaches, Breach{"温度压力", sample.Thermal.Summary})
	}
	return breaches
}
