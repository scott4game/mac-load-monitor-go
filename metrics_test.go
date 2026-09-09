package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseMemoryPressure(t *testing.T) {
	value, err := parseMemoryPressure("System-wide memory free percentage: 56%\n")
	if err != nil || value != 56 {
		t.Fatalf("value=%v err=%v", value, err)
	}
}

func TestCalculateDiskRates(t *testing.T) {
	previous := DiskSnapshot{ReadBytes: 100, WriteBytes: 200, ReadCount: 10, WriteCount: 20, At: time.Unix(0, 0)}
	current := DiskSnapshot{ReadBytes: 2100, WriteBytes: 4200, ReadCount: 30, WriteCount: 60, At: time.Unix(2, 0)}
	rates := calculateDiskRates(previous, current)
	if !rates.Available || rates.ReadBytesPerSecond != 1000 || rates.WriteBytesPerSecond != 2000 || rates.ReadIOPS != 10 || rates.WriteIOPS != 20 {
		t.Fatalf("unexpected rates: %+v", rates)
	}
}

func TestCalculateDiskRatesRejectsCounterReset(t *testing.T) {
	previous := DiskSnapshot{ReadBytes: 200, At: time.Unix(0, 0)}
	current := DiskSnapshot{ReadBytes: 100, At: time.Unix(1, 0)}
	if calculateDiskRates(previous, current).Available {
		t.Fatal("counter reset must invalidate rates")
	}
}

func TestParseThermalStatus(t *testing.T) {
	normal := parseThermalStatus("Note: No thermal warning level has been recorded\nNote: No performance warning level has been recorded\n", 10)
	if !normal.Available || normal.Alert || normal.Summary != "正常" {
		t.Fatalf("unexpected normal status: %+v", normal)
	}
	warning := parseThermalStatus("CPU_Speed_Limit = 70\nScheduler_Limit = 80\nAvailable_CPUs = 8\n", 10)
	if !warning.Alert || !strings.Contains(warning.Summary, "70") {
		t.Fatalf("unexpected warning status: %+v", warning)
	}
}

func TestParseBatteryStatus(t *testing.T) {
	status := parseBatteryStatus("Now drawing from 'AC Power'\n -InternalBattery-0\t80%; AC attached; not charging present: true\n")
	if !status.Available || status.Percent != 80 || status.Source != "AC Power" || status.State != "not charging present: true" {
		t.Fatalf("unexpected battery: %+v", status)
	}
}

func TestParseProcessesKeepsNameWithoutArguments(t *testing.T) {
	processes := parseProcesses(" 42 81.2 204800 /Applications/My App.app/Contents/MacOS/My App\n")
	if len(processes) != 1 || processes[0].Name != "My App" || processes[0].MemoryBytes != 204800*1024 {
		t.Fatalf("unexpected process: %+v", processes)
	}
}

func TestEvaluateBreachesUsesExpectedBoundaries(t *testing.T) {
	config := Config{
		CPUThresholdPercent:             90,
		MemoryAvailableThresholdPercent: 20,
		DiskUsageThresholdPercent:       90,
		ThermalAlertEnabled:             true,
	}
	sample := Sample{
		CPUAvailable:            true,
		CPUPercent:              90,
		MemoryPressureAvailable: true,
		MemoryAvailablePercent:  20,
		DiskAvailable:           true,
		DiskUsedPercent:         90,
		Thermal:                 ThermalStatus{Available: true, Alert: true, Summary: "warning"},
	}
	breaches := EvaluateBreaches(config, sample)
	if len(breaches) != 3 {
		t.Fatalf("expected CPU, disk and thermal breaches, got %+v", breaches)
	}
}
