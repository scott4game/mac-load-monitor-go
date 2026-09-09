package main

import "time"

type DiskSnapshot struct {
	ReadBytes  uint64
	WriteBytes uint64
	ReadCount  uint64
	WriteCount uint64
	At         time.Time
}

type DiskRates struct {
	ReadBytesPerSecond  float64
	WriteBytesPerSecond float64
	ReadIOPS            float64
	WriteIOPS           float64
	Available           bool
}

type ProcessUsage struct {
	PID         int
	CPUPercent  float64
	MemoryBytes uint64
	Name        string
}

type ThermalStatus struct {
	Available bool
	Alert     bool
	Summary   string
}

type BatteryStatus struct {
	Available bool
	Percent   float64
	Source    string
	State     string
}

type Sample struct {
	At                      time.Time
	Hostname                string
	CPUPercent              float64
	CPUAvailable            bool
	LogicalCPUs             int
	Load1                   float64
	Load5                   float64
	Load15                  float64
	LoadAvailable           bool
	MemoryTotalBytes        uint64
	MemoryUsedBytes         uint64
	MemoryAvailableBytes    uint64
	MemoryUsedPercent       float64
	MemoryAvailablePercent  float64
	MemoryCapacityAvailable bool
	MemoryPressureAvailable bool
	DiskTotalBytes          uint64
	DiskUsedBytes           uint64
	DiskFreeBytes           uint64
	DiskUsedPercent         float64
	DiskAvailable           bool
	DiskRates               DiskRates
	Thermal                 ThermalStatus
	Battery                 BatteryStatus
	Uptime                  time.Duration
	UptimeAvailable         bool
	TopCPU                  []ProcessUsage
	TopMemory               []ProcessUsage
	Warnings                []string
}

type Breach struct {
	Metric string
	Detail string
}
