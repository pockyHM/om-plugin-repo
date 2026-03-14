//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dmesg kernel log levels
const (
	dmesgLevelEmerg = 0
	dmesgLevelAlert = 1
	dmesgLevelCrit  = 2
	dmesgLevelErr   = 3
	dmesgLevelWarn  = 4
)

// dmesgPattern maps a kernel message substring to an output type and severity.
type dmesgPattern struct {
	substr   string
	msgType  string
	severity string
}

// Patterns are checked in order; first match wins.
var dmesgPatterns = []dmesgPattern{
	// --- OOM ---
	{"Out of memory:", "alert", "critical"},
	{"oom_kill_process", "alert", "critical"},
	{"Killed process", "alert", "critical"},
	{"page allocation failure", "issue", "warning"},
	// --- Kernel bugs / panics ---
	{"Kernel panic", "alert", "critical"},
	{"BUG: ", "alert", "critical"},
	{"general protection fault", "alert", "critical"},
	{"kernel BUG at", "alert", "critical"},
	// --- Soft/hard lockups ---
	{"soft lockup", "alert", "critical"},
	{"hard LOCKUP", "alert", "critical"},
	{"hung_task", "issue", "warning"},
	{"RCU stall", "issue", "warning"},
	// --- CPU / MCE ---
	{"mce: [Hardware Error]", "alert", "critical"},
	{"Machine check events logged", "issue", "warning"},
	{"APIC error", "issue", "warning"},
	// --- Memory hardware ---
	{"EDAC", "issue", "warning"},
	{"memory corruption", "alert", "critical"},
	// --- Stack / segfault ---
	{"segfault at", "issue", "warning"},
	{"stack-protector:", "issue", "warning"},
	// --- Storage ---
	{"I/O error", "issue", "warning"},
	{"blk_update_request:", "issue", "warning"},
	{"SCSI error", "issue", "warning"},
	// --- Filesystems ---
	{"EXT4-fs error", "issue", "warning"},
	{"XFS (", "issue", "warning"},
	{"BTRFS error", "issue", "warning"},
	// --- Network ---
	{"NFS: server", "issue", "warning"},
	// --- ACPI / firmware ---
	{"ACPI Error:", "issue", "warning"},
	{"firmware bug:", "issue", "warning"},
}

// watchDmesg opens /dev/kmsg, seeks to the end of the ring buffer to skip
// historical messages, and then streams new kernel messages.
// Requires CAP_SYSLOG or root on kernels with dmesg_restrict=1.
func watchDmesg() {
	fd, err := syscall.Open("/dev/kmsg", syscall.O_RDONLY, 0)
	if err != nil {
		emitError(fmt.Sprintf("dmesg: cannot open /dev/kmsg: %v (requires CAP_SYSLOG or root)", err))
		return
	}

	// SEEK_END on /dev/kmsg positions the read pointer after the last record
	// so only new kernel messages are returned.
	if _, err := syscall.Seek(fd, 0, 2 /* SEEK_END */); err != nil {
		// Some older kernels don't support seeking; proceed from current position
		// and deduplicate via timestamps if needed.
		emitEvent("dmesg: seek to end failed, reading from ring buffer start", nil)
	}

	f := os.NewFile(uintptr(fd), "/dev/kmsg")
	defer f.Close()

	emitEvent("dmesg watcher started", nil)

	buf := make([]byte, 8192)
	for {
		n, err := f.Read(buf)
		if err != nil {
			emitError(fmt.Sprintf("dmesg: read error: %v", err))
			// Brief pause before retry in case of transient error
			time.Sleep(time.Second)
			continue
		}
		if n > 0 {
			line := strings.TrimRight(string(buf[:n]), "\n\x00")
			processDmesgLine(line)
		}
	}
}

// processDmesgLine parses one /dev/kmsg record.
//
// Format: priority,seqno,timestamp_usec,flags[,continuation];MESSAGE
//
//	priority encodes facility<<3 | level in lower 3 bits.
func processDmesgLine(line string) {
	// Split header and message at the first semicolon.
	idx := strings.IndexByte(line, ';')
	if idx < 0 {
		return
	}
	header := line[:idx]
	message := strings.TrimSpace(line[idx+1:])
	if message == "" {
		return
	}

	// Parse log level from first header field.
	parts := strings.SplitN(header, ",", 3)
	level := -1
	if len(parts) >= 1 {
		if v, err := strconv.Atoi(parts[0]); err == nil {
			level = v & 7 // lower 3 bits
		}
	}

	// Check against known critical patterns.
	for _, p := range dmesgPatterns {
		if strings.Contains(message, p.substr) {
			key := "dmesg_pattern_" + p.substr
			tid := fmt.Sprintf("dmesg-%s-%d",
				strings.ReplaceAll(strings.ToLower(p.substr), " ", "-"),
				time.Now().Unix())
			labels := map[string]string{
				"severity": p.severity,
				"source":   "kernel",
				"pattern":  p.substr,
			}
			if p.msgType == "alert" {
				if canAlert(key) {
					emitAlert("[kernel] "+message, labels, tid)
				}
			} else {
				if canAlert(key) {
					emitIssue("[kernel] "+message, labels, tid)
				}
			}
			return // only the first matching pattern fires
		}
	}

	// Emit any kernel error/critical that didn't match a named pattern.
	if level >= 0 && level <= dmesgLevelErr {
		msgKey := message
		if len(msgKey) > 40 {
			msgKey = msgKey[:40]
		}
		if canAlert("dmesg_lvl_" + msgKey) {
			labels := map[string]string{
				"severity": "warning",
				"source":   "kernel",
				"level":    strconv.Itoa(level),
			}
			emitIssue("[kernel] "+message, labels, "")
		}
	}
}
