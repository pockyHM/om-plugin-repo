package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

var config *Config

func main() {
	config = loadConfig()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	collectSysInfo()
	collectMetrics()
	emitEvent("om-node-monitor started", nil)

	go watchDmesg()
	go watchSSH()

	metricsTicker := time.NewTicker(time.Duration(config.MetricsInterval) * time.Second)
	labelsTicker := time.NewTicker(time.Duration(config.LabelsInterval) * time.Second)
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
