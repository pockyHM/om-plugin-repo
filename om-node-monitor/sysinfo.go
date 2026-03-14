package main

import (
	"runtime"

	"github.com/shirou/gopsutil/v3/host"
)

// collectSysInfo emits a "labels" message with static node information
// and uptime/process count metrics.
func collectSysInfo() {
	info, err := host.Info()
	if err != nil {
		emitError("host.Info: " + err.Error())
		return
	}

	labels := map[string]string{
		"hostname":         info.Hostname,
		"os":               info.OS,
		"platform":         info.Platform,
		"platform_version": info.PlatformVersion,
		"kernel_version":   info.KernelVersion,
		"arch":             runtime.GOARCH,
	}
	if info.VirtualizationSystem != "" {
		labels["virt_system"] = info.VirtualizationSystem
		labels["virt_role"] = info.VirtualizationRole
	}

	emitLabels(labels)

	emitMetric(float64(info.Uptime), map[string]string{
		"metric_name": "system_uptime_seconds",
		"unit":        "seconds",
	})
	emitMetric(float64(info.Procs), map[string]string{
		"metric_name": "system_processes_total",
	})
	emitMetric(float64(info.BootTime), map[string]string{
		"metric_name": "system_boot_time_seconds",
	})
}
