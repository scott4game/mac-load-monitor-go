package main

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

type fakeCollector struct {
	samples []Sample
	index   int
	resets  int
}

func (collector *fakeCollector) Collect(context.Context) Sample {
	sample := collector.samples[collector.index]
	collector.index++
	return sample
}

func (collector *fakeCollector) AddTopProcesses(context.Context, *Sample, int) {}
func (collector *fakeCollector) ResetDiskBaseline()                            { collector.resets++ }

type fakeNotifier struct {
	messages []string
}

func (notifier *fakeNotifier) Send(_ context.Context, message string) bool {
	notifier.messages = append(notifier.messages, message)
	return true
}

func monitorTestConfig() Config {
	return Config{
		Keyword:                         "Mac 负载监控",
		CPUThresholdPercent:             90,
		MemoryAvailableThresholdPercent: 20,
		DiskUsageThresholdPercent:       90,
		CheckInterval:                   10 * time.Minute,
		ReportInterval:                  time.Hour,
	}
}

func cpuSample(percent float64) Sample {
	return Sample{At: time.Now(), Hostname: "test", CPUAvailable: true, CPUPercent: percent}
}

func TestEveryOverloadedSampleSendsAndRecoverySendsOnce(t *testing.T) {
	collector := &fakeCollector{samples: []Sample{cpuSample(95), cpuSample(96), cpuSample(10), cpuSample(10)}}
	notifier := &fakeNotifier{}
	monitor := NewMonitor(monitorTestConfig(), collector, notifier, log.New(io.Discard, "", 0))
	monitor.collectAndHandle(context.Background(), false)
	monitor.collectAndHandle(context.Background(), false)
	monitor.collectAndHandle(context.Background(), false)
	monitor.collectAndHandle(context.Background(), false)
	if len(notifier.messages) != 3 {
		t.Fatalf("expected two alerts and one recovery, got %d", len(notifier.messages))
	}
}

func TestHourlyOverloadProducesOneCombinedMessage(t *testing.T) {
	collector := &fakeCollector{samples: []Sample{cpuSample(95)}}
	notifier := &fakeNotifier{}
	monitor := NewMonitor(monitorTestConfig(), collector, notifier, log.New(io.Discard, "", 0))
	monitor.collectAndHandle(context.Background(), true)
	if len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0], "每小时状态 | 超负荷") {
		t.Fatalf("unexpected messages: %+v", notifier.messages)
	}
}

func TestHourlyNormalSampleSends(t *testing.T) {
	collector := &fakeCollector{samples: []Sample{cpuSample(10)}}
	notifier := &fakeNotifier{}
	monitor := NewMonitor(monitorTestConfig(), collector, notifier, log.New(io.Discard, "", 0))
	monitor.collectAndHandle(context.Background(), true)
	if len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0], "每小时状态") {
		t.Fatalf("unexpected messages: %+v", notifier.messages)
	}
}

func TestNextAlignedUsesWallClockBoundary(t *testing.T) {
	location := time.FixedZone("SGT", 8*60*60)
	now := time.Date(2026, 9, 9, 19, 43, 12, 0, location)
	if got := nextAligned(now, 10*time.Minute); !got.Equal(time.Date(2026, 9, 9, 19, 50, 0, 0, location)) {
		t.Fatalf("unexpected check boundary: %s", got)
	}
	if got := nextAligned(now, time.Hour); !got.Equal(time.Date(2026, 9, 9, 20, 0, 0, 0, location)) {
		t.Fatalf("unexpected report boundary: %s", got)
	}
}

func TestSamplingDiscontinuityDetectsSleepAndClockRollback(t *testing.T) {
	previous := time.Date(2026, 9, 9, 19, 0, 0, 0, time.Local)
	if !isSamplingDiscontinuity(previous, previous.Add(16*time.Minute), 10*time.Minute) {
		t.Fatal("expected long gap to be a discontinuity")
	}
	if isSamplingDiscontinuity(previous, previous.Add(10*time.Minute), 10*time.Minute) {
		t.Fatal("expected normal interval")
	}
	if !isSamplingDiscontinuity(previous, previous.Add(-time.Minute), 10*time.Minute) {
		t.Fatal("expected clock rollback to be a discontinuity")
	}
}
