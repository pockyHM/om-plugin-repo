package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	metricsInterval = 15 * time.Second
	labelsInterval  = 5 * time.Minute
)

func main() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	// Collect and emit initial system info labels.
	collectSysInfo()

	// Initial metrics collection before the first tick.
	collectMetrics()

	emitEvent("om-node-monitor started", nil)

	// Start background log watchers (non-blocking goroutines).
	go watchDmesg()
	go watchSSH()

	metricsTicker := time.NewTicker(metricsInterval)
	labelsTicker := time.NewTicker(labelsInterval)
	defer metricsTicker.Stop()
	defer labelsTicker.Stop()

	for {
		select {
		case <-quit:
			emitEvent("om-node-monitor stopped gracefully", nil)
			os.Exit(0)
		case <-metricsTicker.C:
			collectMetrics()
		case <-labelsTicker.C:
			collectSysInfo()
		}
	}
}
