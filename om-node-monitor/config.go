package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	MetricsInterval         int     `yaml:"metrics_interval"`
	LabelsInterval          int     `yaml:"labels_interval"`
	CPUWarnThreshold        float64 `yaml:"cpu_warn_threshold"`
	CPUCritThreshold        float64 `yaml:"cpu_crit_threshold"`
	MemoryWarnThreshold     float64 `yaml:"memory_warn_threshold"`
	MemoryCritThreshold     float64 `yaml:"memory_crit_threshold"`
	DiskWarnThreshold       float64 `yaml:"disk_warn_threshold"`
	DiskCritThreshold       float64 `yaml:"disk_crit_threshold"`
	SSHBruteForceThreshold  int     `yaml:"ssh_brute_force_threshold"`
	SSHBruteForceWindow     int     `yaml:"ssh_brute_force_window"`
}

func loadConfig() *Config {
	cfg := &Config{
		MetricsInterval:         15,
		LabelsInterval:          300,
		CPUWarnThreshold:        80.0,
		CPUCritThreshold:        90.0,
		MemoryWarnThreshold:     80.0,
		MemoryCritThreshold:     90.0,
		DiskWarnThreshold:       80.0,
		DiskCritThreshold:       90.0,
		SSHBruteForceThreshold:  10,
		SSHBruteForceWindow:     300,
	}

	data, err := os.ReadFile("config-values.yaml")
	if err != nil {
		return cfg
	}

	yaml.Unmarshal(data, cfg)
	return cfg
}
