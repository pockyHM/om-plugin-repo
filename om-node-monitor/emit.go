package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const sourceName = "om-node-monitor"

// Message is the plugin wire protocol message (NDJSON on stdout).
type Message struct {
	Source    string            `json:"source"`
	Type      string            `json:"type"`
	Timestamp int64             `json:"timestamp"`
	Message   string            `json:"message,omitempty"`
	Value     *float64          `json:"value,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	TID       string            `json:"tid,omitempty"`
}

var stdoutMu sync.Mutex

func emit(msg Message) {
	msg.Source = sourceName
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().Unix()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	stdoutMu.Lock()
	fmt.Println(string(data))
	stdoutMu.Unlock()
}

func f64(v float64) *float64 { return &v }

func emitMetric(value float64, labels map[string]string) {
	emit(Message{Type: "metric", Value: f64(value), Labels: labels})
}

func emitAlert(message string, labels map[string]string, tid string) {
	emit(Message{Type: "alert", Message: message, Labels: labels, TID: tid})
}

func emitEvent(message string, labels map[string]string) {
	emit(Message{Type: "event", Message: message, Labels: labels})
}

func emitIssue(message string, labels map[string]string, tid string) {
	emit(Message{Type: "issue", Message: message, Labels: labels, TID: tid})
}

func emitError(message string) {
	emit(Message{Type: "error", Message: message})
}

func emitLabels(labels map[string]string) {
	emit(Message{Type: "labels", Labels: labels})
}

// mergeMaps returns a new map with entries from both a and b (b wins on conflict).
func mergeMaps(a, b map[string]string) map[string]string {
	result := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

// --- alert cooldown ----------------------------------------------------------

const alertCooldownDur = 5 * time.Minute

var cooldownMu sync.Mutex
var cooldowns = make(map[string]time.Time)

// canAlert returns true if the cooldown for key has expired (and resets the timer).
func canAlert(key string) bool {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	last, ok := cooldowns[key]
	if !ok || time.Since(last) >= alertCooldownDur {
		cooldowns[key] = time.Now()
		return true
	}
	return false
}
