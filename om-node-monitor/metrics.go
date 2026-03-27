package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	psnet "github.com/shirou/gopsutil/v3/net"
)

const swapWarnThreshold = 70.0

// Pseudo-filesystem types to skip when reporting disk usage.
var skipFsTypes = map[string]bool{
	"sysfs": true, "proc": true, "devfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "pstore": true, "bpf": true,
	"debugfs": true, "tracefs": true, "hugetlbfs": true, "mqueue": true,
	"fusectl": true, "securityfs": true, "squashfs": true,
	"overlay": true, "nsfs": true, "rpc_pipefs": true,
}

// collectCPU emits per-core and total CPU usage, load averages, and CPU count.
// It blocks for ~1 second to get an accurate measurement.
func collectCPU() {
	perCore, err := cpu.Percent(time.Second, true)
	if err != nil {
		emitError("cpu.Percent: " + err.Error())
		return
	}

	var total float64
	for i, p := range perCore {
		emitMetric(p, map[string]string{
			"metric_name": "cpu_usage_percent",
			"cpu":         fmt.Sprintf("cpu%d", i),
			"unit":        "percent",
		})
		total += p
	}

	if len(perCore) > 0 {
		total /= float64(len(perCore))
		emitMetric(total, map[string]string{
			"metric_name": "cpu_usage_percent",
			"cpu":         "total",
			"unit":        "percent",
		})

		switch {
		case total >= config.CPUCritThreshold && canAlert("cpu_crit"):
			emitAlert(
				fmt.Sprintf("CPU usage %.1f%% exceeds critical threshold %.0f%%", total, config.CPUCritThreshold),
				map[string]string{"severity": "critical", "metric": "cpu_usage_percent"},
				fmt.Sprintf("cpu-high-%d", time.Now().Unix()),
			)
		case total >= config.CPUWarnThreshold && canAlert("cpu_warn"):
			emitAlert(
				fmt.Sprintf("CPU usage %.1f%% exceeds warning threshold %.0f%%", total, config.CPUWarnThreshold),
				map[string]string{"severity": "warning", "metric": "cpu_usage_percent"},
				"",
			)
		}
	}

	// CPU logical count
	if n, err := cpu.Counts(true); err == nil {
		emitMetric(float64(n), map[string]string{"metric_name": "cpu_count_logical"})
	}

	// Load averages (Linux/macOS only; returns error on Windows).
	if avg, err := load.Avg(); err == nil {
		emitMetric(avg.Load1, map[string]string{"metric_name": "load_avg_1m"})
		emitMetric(avg.Load5, map[string]string{"metric_name": "load_avg_5m"})
		emitMetric(avg.Load15, map[string]string{"metric_name": "load_avg_15m"})
	}
}

// collectMemory emits RAM and swap usage metrics.
func collectMemory() {
	vm, err := mem.VirtualMemory()
	if err != nil {
		emitError("mem.VirtualMemory: " + err.Error())
		return
	}

	emitMetric(float64(vm.Total), map[string]string{"metric_name": "mem_total_bytes", "unit": "bytes"})
	emitMetric(float64(vm.Used), map[string]string{"metric_name": "mem_used_bytes", "unit": "bytes"})
	emitMetric(float64(vm.Available), map[string]string{"metric_name": "mem_available_bytes", "unit": "bytes"})
	emitMetric(float64(vm.Buffers), map[string]string{"metric_name": "mem_buffers_bytes", "unit": "bytes"})
	emitMetric(float64(vm.Cached), map[string]string{"metric_name": "mem_cached_bytes", "unit": "bytes"})
	emitMetric(vm.UsedPercent, map[string]string{"metric_name": "mem_used_percent", "unit": "percent"})

	switch {
	case vm.UsedPercent >= config.MemoryCritThreshold && canAlert("mem_crit"):
		emitAlert(
			fmt.Sprintf("Memory usage %.1f%% exceeds critical threshold %.0f%%", vm.UsedPercent, config.MemoryCritThreshold),
			map[string]string{"severity": "critical", "metric": "mem_used_percent"},
			fmt.Sprintf("mem-high-%d", time.Now().Unix()),
		)
	case vm.UsedPercent >= config.MemoryWarnThreshold && canAlert("mem_warn"):
		emitAlert(
			fmt.Sprintf("Memory usage %.1f%% exceeds warning threshold %.0f%%", vm.UsedPercent, config.MemoryWarnThreshold),
			map[string]string{"severity": "warning", "metric": "mem_used_percent"},
			"",
		)
	}

	// Swap
	sw, err := mem.SwapMemory()
	if err == nil && sw.Total > 0 {
		emitMetric(float64(sw.Total), map[string]string{"metric_name": "swap_total_bytes", "unit": "bytes"})
		emitMetric(float64(sw.Used), map[string]string{"metric_name": "swap_used_bytes", "unit": "bytes"})
		emitMetric(sw.UsedPercent, map[string]string{"metric_name": "swap_used_percent", "unit": "percent"})

		if sw.UsedPercent >= swapWarnThreshold && canAlert("swap_warn") {
			emitAlert(
				fmt.Sprintf("Swap usage %.1f%% exceeds warning threshold %.0f%%", sw.UsedPercent, swapWarnThreshold),
				map[string]string{"severity": "warning", "metric": "swap_used_percent"},
				"",
			)
		}
	}
}

// collectDisk emits per-partition usage and per-device I/O counters.
func collectDisk() {
	parts, err := disk.Partitions(false)
	if err != nil {
		emitError("disk.Partitions: " + err.Error())
		return
	}

	for _, p := range parts {
		if skipFsTypes[p.Fstype] {
			continue
		}

		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}

		baseLabels := map[string]string{
			"mount":  p.Mountpoint,
			"device": p.Device,
			"fstype": p.Fstype,
		}

		emitMetric(float64(usage.Total), mergeMaps(baseLabels, map[string]string{
			"metric_name": "disk_total_bytes", "unit": "bytes",
		}))
		emitMetric(float64(usage.Used), mergeMaps(baseLabels, map[string]string{
			"metric_name": "disk_used_bytes", "unit": "bytes",
		}))
		emitMetric(float64(usage.Free), mergeMaps(baseLabels, map[string]string{
			"metric_name": "disk_free_bytes", "unit": "bytes",
		}))
		emitMetric(usage.UsedPercent, mergeMaps(baseLabels, map[string]string{
			"metric_name": "disk_used_percent", "unit": "percent",
		}))
		if usage.InodesTotal > 0 {
			emitMetric(float64(usage.InodesUsed), mergeMaps(baseLabels, map[string]string{
				"metric_name": "disk_inodes_used",
			}))
			inodesPercent := float64(usage.InodesUsed) / float64(usage.InodesTotal) * 100
			emitMetric(inodesPercent, mergeMaps(baseLabels, map[string]string{
				"metric_name": "disk_inodes_used_percent", "unit": "percent",
			}))
		}

		alertKey := "disk_" + strings.ReplaceAll(p.Mountpoint, "/", "_")
		switch {
		case usage.UsedPercent >= config.DiskCritThreshold && canAlert(alertKey+"_crit"):
			emitAlert(
				fmt.Sprintf("Disk %s usage %.1f%% exceeds critical threshold %.0f%%",
					p.Mountpoint, usage.UsedPercent, config.DiskCritThreshold),
				mergeMaps(baseLabels, map[string]string{"severity": "critical"}),
				fmt.Sprintf("disk-high%s-%d", strings.ReplaceAll(p.Mountpoint, "/", "-"), time.Now().Unix()),
			)
		case usage.UsedPercent >= config.DiskWarnThreshold && canAlert(alertKey+"_warn"):
			emitAlert(
				fmt.Sprintf("Disk %s usage %.1f%% exceeds warning threshold %.0f%%",
					p.Mountpoint, usage.UsedPercent, config.DiskWarnThreshold),
				mergeMaps(baseLabels, map[string]string{"severity": "warning"}),
				"",
			)
		}
	}

	// Disk I/O counters (cumulative since boot — use as counters in VictoriaMetrics).
	ioCounters, err := disk.IOCounters()
	if err == nil {
		for dev, stat := range ioCounters {
			labels := map[string]string{"device": dev}
			emitMetric(float64(stat.ReadBytes), mergeMaps(labels, map[string]string{
				"metric_name": "disk_read_bytes_total", "unit": "bytes",
			}))
			emitMetric(float64(stat.WriteBytes), mergeMaps(labels, map[string]string{
				"metric_name": "disk_write_bytes_total", "unit": "bytes",
			}))
			emitMetric(float64(stat.ReadCount), mergeMaps(labels, map[string]string{
				"metric_name": "disk_read_ops_total",
			}))
			emitMetric(float64(stat.WriteCount), mergeMaps(labels, map[string]string{
				"metric_name": "disk_write_ops_total",
			}))
			emitMetric(float64(stat.ReadTime), mergeMaps(labels, map[string]string{
				"metric_name": "disk_read_time_ms_total", "unit": "ms",
			}))
			emitMetric(float64(stat.WriteTime), mergeMaps(labels, map[string]string{
				"metric_name": "disk_write_time_ms_total", "unit": "ms",
			}))
		}
	}
}

// collectNetwork emits per-interface I/O counters and TCP connection counts.
func collectNetwork() {
	ifStats, err := psnet.IOCounters(true)
	if err != nil {
		emitError("net.IOCounters: " + err.Error())
		return
	}

	for _, stat := range ifStats {
		if stat.Name == "lo" || stat.Name == "Loopback Pseudo-Interface 1" {
			continue
		}
		labels := map[string]string{"interface": stat.Name}
		emitMetric(float64(stat.BytesSent), mergeMaps(labels, map[string]string{
			"metric_name": "net_bytes_sent_total", "unit": "bytes",
		}))
		emitMetric(float64(stat.BytesRecv), mergeMaps(labels, map[string]string{
			"metric_name": "net_bytes_recv_total", "unit": "bytes",
		}))
		emitMetric(float64(stat.PacketsSent), mergeMaps(labels, map[string]string{
			"metric_name": "net_packets_sent_total",
		}))
		emitMetric(float64(stat.PacketsRecv), mergeMaps(labels, map[string]string{
			"metric_name": "net_packets_recv_total",
		}))
		emitMetric(float64(stat.Errin), mergeMaps(labels, map[string]string{
			"metric_name": "net_errors_in_total",
		}))
		emitMetric(float64(stat.Errout), mergeMaps(labels, map[string]string{
			"metric_name": "net_errors_out_total",
		}))
		emitMetric(float64(stat.Dropin), mergeMaps(labels, map[string]string{
			"metric_name": "net_drops_in_total",
		}))
		emitMetric(float64(stat.Dropout), mergeMaps(labels, map[string]string{
			"metric_name": "net_drops_out_total",
		}))
	}

	// TCP connection state summary
	conns, err := psnet.Connections("tcp")
	if err == nil {
		stateCounts := make(map[string]int)
		for _, c := range conns {
			stateCounts[c.Status]++
		}
		for state, count := range stateCounts {
			emitMetric(float64(count), map[string]string{
				"metric_name": "net_tcp_connections",
				"state":       state,
			})
		}
	}
}

// collectMetrics runs all metric collectors sequentially.
func collectMetrics() {
	collectCPU()
	collectMemory()
	collectDisk()
	collectNetwork()
}
