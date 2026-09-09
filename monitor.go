package main

import (
	"context"
	"log"
	"time"
)

type sampleCollector interface {
	Collect(context.Context) Sample
	AddTopProcesses(context.Context, *Sample, int)
	ResetDiskBaseline()
}

type messageNotifier interface {
	Send(context.Context, string) bool
}

type Monitor struct {
	config        Config
	collector     sampleCollector
	notifier      messageNotifier
	logger        *log.Logger
	now           func() time.Time
	wasOverloaded bool
	lastSampleAt  time.Time
}

func NewMonitor(config Config, collector sampleCollector, notifier messageNotifier, logger *log.Logger) *Monitor {
	return &Monitor{
		config:    config,
		collector: collector,
		notifier:  notifier,
		logger:    logger,
		now:       time.Now,
	}
}

func (monitor *Monitor) Run(ctx context.Context) error {
	monitor.logger.Printf("INFO 监控启动：每 %s 检测，每 %s 报告", monitor.config.CheckInterval, monitor.config.ReportInterval)
	monitor.collectAndHandle(ctx, false)
	now := monitor.now()
	nextCheck := nextAligned(now, monitor.config.CheckInterval)
	nextReport := nextAligned(now, monitor.config.ReportInterval)

	for {
		nextWake := nextCheck
		if nextReport.Before(nextWake) {
			nextWake = nextReport
		}
		timer := time.NewTimer(time.Until(nextWake))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			monitor.logger.Printf("INFO 监控停止")
			return nil
		case <-timer.C:
		}

		now = monitor.now()
		checkDue := !now.Before(nextCheck)
		reportDue := !now.Before(nextReport)
		if isSamplingDiscontinuity(monitor.lastSampleAt, now, monitor.config.CheckInterval) {
			monitor.collector.ResetDiskBaseline()
			checkDue = true
			monitor.logger.Printf("INFO 检测到睡眠或采样中断，已重置磁盘吞吐基线")
		}
		if checkDue || reportDue {
			monitor.collectAndHandle(ctx, reportDue)
		}
		for !nextCheck.After(now) {
			nextCheck = nextCheck.Add(monitor.config.CheckInterval)
		}
		for !nextReport.After(now) {
			nextReport = nextReport.Add(monitor.config.ReportInterval)
		}
	}
}

func (monitor *Monitor) collectAndHandle(ctx context.Context, reportDue bool) {
	sample := monitor.collector.Collect(ctx)
	monitor.lastSampleAt = monitor.now()
	breaches := EvaluateBreaches(monitor.config, sample)
	overloaded := len(breaches) > 0
	shouldSend := overloaded || reportDue || (monitor.wasOverloaded && !overloaded)
	if !shouldSend {
		monitor.logger.Printf("INFO 检测完成：CPU %.1f%%，内存可用评分 %.1f%%，磁盘 %.1f%%", sample.CPUPercent, sample.MemoryAvailablePercent, sample.DiskUsedPercent)
		return
	}
	monitor.collector.AddTopProcesses(ctx, &sample, monitor.config.TopProcessCount)
	status := "每小时状态"
	switch {
	case reportDue && overloaded:
		status = "每小时状态 | 超负荷"
	case overloaded:
		status = "超负荷"
	case monitor.wasOverloaded:
		status = "已恢复"
	}
	sent := monitor.notifier.Send(ctx, FormatMessage(monitor.config, sample, status, breaches))
	monitor.logger.Printf("INFO %s推送%s", status, map[bool]string{true: "成功", false: "失败"}[sent])
	monitor.wasOverloaded = overloaded
}

func isSamplingDiscontinuity(previous, current time.Time, interval time.Duration) bool {
	return !previous.IsZero() && (current.Before(previous) || current.Sub(previous) > interval*3/2)
}

func nextAligned(now time.Time, interval time.Duration) time.Time {
	local := now.In(now.Location())
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	elapsed := local.Sub(midnight)
	if interval >= 24*time.Hour {
		return local.Add(interval)
	}
	return midnight.Add((elapsed/interval + 1) * interval)
}
