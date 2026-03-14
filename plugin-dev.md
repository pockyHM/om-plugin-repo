# Plugin Development Guide

OpenManager 插件是运行在被管节点上的**独立子进程**，通过 stdin/stdout JSON 消息与 Agent 通信。插件可以用任意语言编写，只需能读写标准输入输出即可。

---

## 目录

1. [工作原理](#1-工作原理)
2. [插件包结构](#2-插件包结构)
3. [plugin.json 字段说明](#3-pluginjson-字段说明)
4. [消息协议](#4-消息协议)
5. [消息类型详解](#5-消息类型详解)
6. [启动与生命周期](#6-启动与生命周期)
7. [完整示例（Go）](#7-完整示例go)
8. [完整示例（Python）](#8-完整示例python)
9. [完整示例（Shell）](#9-完整示例shell)
10. [打包与发布](#10-打包与发布)
11. [仓库源 index.json 格式](#11-仓库源-indexjson-格式)
12. [调试技巧](#12-调试技巧)

---

## 1. 工作原理

```
Plugin Process
  │  stdout → JSON 消息（每行一条）
  │  stdin  ← 未来扩展（Agent 下发指令）
  ↓
Agent (om-agent)
  │  解析消息，按 type 路由
  │  metric  → push 到 VictoriaMetrics
  │  alert / event / error / issue / log → 上报到 Manager
  ↓
Manager (om-manager)
  │  持久化到 PostgreSQL
  │  触发告警分析 / AI 运维会话
```

**关键点：**

- 插件以**子进程**方式被 Agent 启动，Agent 负责生命周期（启动/重启/停止）
- 插件只需写 stdout，每行一条 JSON，**不需要任何网络连接**
- Agent 启动失败会自动重试 3 次（5s → 15s → 30s 延迟）
- Agent 通过 `plugin.pid` 文件追踪进程，重启时清理残留进程

---

## 2. 插件包结构

插件以 **zip 格式**分发，解压后目录结构如下：

```
my-plugin/
├── plugin.json        # 必须：插件元数据
├── start.sh           # 必须（Linux/macOS）：启动脚本
├── start.cmd          # 必须（Windows）：启动脚本
├── my-plugin          # 插件可执行文件（或脚本入口）
└── ...                # 其他资源文件
```

> **注意：** zip 内可以有一层顶级目录（即 `my-plugin/plugin.json` 或直接 `plugin.json`，两种方式均支持）。

### start.sh 示例

```bash
#!/bin/bash
cd "$(dirname "$0")"
exec ./my-plugin
```

```bash
chmod +x start.sh my-plugin
```

### start.cmd 示例（Windows）

```bat
@echo off
cd /d "%~dp0"
my-plugin.exe
```

---

## 3. plugin.json 字段说明

```json
{
  "name": "cpu-monitor",
  "version": "1.0.0",
  "display_name": "CPU Monitor",
  "description": "Monitor CPU usage and push metrics every 10s",
  "author": "your-org",
  "logo": "logo.png",
  "min_agent_version": "0.1.0",
  "os": ["linux", "darwin"],
  "arch": ["amd64", "arm64"],
  "message_types": ["metric", "alert"],
  "skills": ["linux-ops"],
  "tags": ["monitoring", "cpu", "system"]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | ✅ | 插件唯一标识，小写字母+连字符，最长 128 字符 |
| `version` | string | ✅ | 语义版本号，如 `1.0.0` |
| `display_name` | string | | 展示名称（UI 显示用） |
| `description` | string | | 插件功能描述 |
| `author` | string | | 作者或组织名 |
| `logo` | string | | logo 文件名（相对包内路径）或 URL |
| `min_agent_version` | string | | 最低 Agent 版本要求 |
| `os` | []string | | 支持的操作系统：`linux`、`darwin`、`windows` |
| `arch` | []string | | 支持的架构：`amd64`、`arm64` |
| `message_types` | []string | | 插件会发送的消息类型（用于 UI 展示和 AI 理解） |
| `skills` | []string | | 插件依赖的 Skill 名称（Agent 会预加载对应 skill） |
| `tags` | []string | | 分类标签（市场筛选用） |

---

## 4. 消息协议

插件向 stdout 输出 **换行分隔的 JSON**（NDJSON），每行一条消息：

```json
{"source":"cpu-monitor","type":"metric","timestamp":1741910400,"value":78.5,"labels":{"host":"agent01","cpu":"total"}}
{"source":"cpu-monitor","type":"alert","timestamp":1741910400,"message":"CPU usage exceeded 90%","tid":"abc123","labels":{"host":"agent01"}}
```

### 消息字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `source` | string | ✅ | 插件名称，与 `plugin.json` 的 `name` 一致 |
| `type` | string | ✅ | 消息类型，见下表 |
| `timestamp` | int64 | ✅ | Unix 时间戳（秒） |
| `message` | string | | 文本内容（alert/event/error/log/issue 必填） |
| `value` | float64 | | 数值（metric 必填） |
| `labels` | object | | 任意 key-value 标签，用于 Prometheus 标签或上下文 |
| `tid` | string | | Trace ID，同一事件的所有消息使用相同 tid，便于告警关联 |
| `skills` | []string | | 本消息激活的 Skill（影响 AI 运维会话的知识范围） |

---

## 5. 消息类型详解

### `metric` — 数值指标

上报给 VictoriaMetrics，支持 Prometheus 查询。

```json
{
  "source": "cpu-monitor",
  "type": "metric",
  "timestamp": 1741910400,
  "value": 78.5,
  "labels": {
    "host": "agent01",
    "cpu": "total",
    "unit": "percent"
  }
}
```

- `value` 必填，为浮点数
- `labels` 中的 `host` 会自动补全为当前节点 hostname（如不提供）
- 指标名在 VictoriaMetrics 中为：`{source}_{label_key}` 或由 Agent 统一命名

---

### `alert` — 需要人工关注的告警

触发 Manager 告警流程（AI 分析 → DingTalk 通知 → 人工处理）。

```json
{
  "source": "cpu-monitor",
  "type": "alert",
  "timestamp": 1741910400,
  "message": "CPU usage 95.2% exceeds threshold 90% for 5 minutes",
  "tid": "cpu-high-20260314-001",
  "labels": {
    "host": "agent01",
    "severity": "critical",
    "threshold": "90"
  }
}
```

- `message` 尽量包含具体数值和上下文，AI 会直接引用
- `tid` 用同一个 ID 串联同一故障的多条消息，便于 AI 汇总分析
- `labels.severity` 建议填写：`info` / `warning` / `critical`

---

### `event` — 系统事件（无需立即处理）

```json
{
  "source": "process-monitor",
  "type": "event",
  "timestamp": 1741910400,
  "message": "nginx restarted, new PID: 12345",
  "labels": {
    "process": "nginx",
    "action": "restart"
  }
}
```

---

### `error` — 插件自身错误

```json
{
  "source": "cpu-monitor",
  "type": "error",
  "timestamp": 1741910400,
  "message": "failed to read /proc/stat: permission denied"
}
```

---

### `log` — 结构化日志

```json
{
  "source": "app-log-watcher",
  "type": "log",
  "timestamp": 1741910400,
  "message": "[ERROR] database connection timeout after 30s",
  "labels": {
    "level": "error",
    "app": "myapp",
    "file": "/var/log/myapp/app.log"
  }
}
```

---

### `issue` — 异常问题（非立即告警，累积分析）

```json
{
  "source": "disk-monitor",
  "type": "issue",
  "timestamp": 1741910400,
  "message": "Disk /data usage at 85%, trending up 2% per hour",
  "labels": {
    "mount": "/data",
    "usage_percent": "85",
    "trend": "increasing"
  }
}
```

---

### `labels` — 节点标签更新

更新 Manager 中该节点的 labels，供 AI 运维和过滤使用。

```json
{
  "source": "system-info",
  "type": "labels",
  "timestamp": 1741910400,
  "labels": {
    "kernel": "5.15.0-88-generic",
    "distro": "Ubuntu 22.04",
    "docker_version": "24.0.5"
  }
}
```

---

## 6. 启动与生命周期

```
Agent 启动
  └─► 扫描 plugins/ 目录，加载所有含 plugin.json 的子目录
      └─► 执行 start.sh（Linux）或 start.cmd（Windows）
          └─► 插件进程启动，开始向 stdout 写消息
              │
              ├─► 插件崩溃 → Agent 自动重试（最多 3 次：5s/15s/30s）
              └─► 超出重试 → 状态标记为 failed，记录错误
```

**插件应当：**

- 在死循环中持续采集数据并输出消息
- 优雅处理 `SIGTERM` / `SIGINT`（清理资源后退出）
- 不依赖网络端口（所有通信走 stdout）
- 遇到不可恢复错误时输出 `error` 消息后退出（Agent 会重启）

**插件不应当：**

- 在 stdout 输出非 JSON 内容（会被 Agent 丢弃并记录警告）
- 无限期挂起（无输出超过 Agent 配置的超时时间会被判定为异常）
- 读取 stdin 并阻塞（stdin 目前不保证有数据）

---

## 7. 完整示例（Go）

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"
)

type Message struct {
    Source    string            `json:"source"`
    Type      string            `json:"type"`
    Timestamp int64             `json:"timestamp"`
    Message   string            `json:"message,omitempty"`
    Value     *float64          `json:"value,omitempty"`
    Labels    map[string]string `json:"labels,omitempty"`
    TID       string            `json:"tid,omitempty"`
}

func emit(msg Message) {
    msg.Timestamp = time.Now().Unix()
    b, _ := json.Marshal(msg)
    fmt.Println(string(b))
}

func f64(v float64) *float64 { return &v }

func cpuUsage() float64 {
    // TODO: 读取 /proc/stat 计算 CPU 使用率
    return 42.0
}

func main() {
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-quit:
            emit(Message{Source: "cpu-monitor", Type: "event", Message: "plugin stopped gracefully"})
            os.Exit(0)
        case <-ticker.C:
            usage := cpuUsage()
            emit(Message{
                Source: "cpu-monitor",
                Type:   "metric",
                Value:  f64(usage),
                Labels: map[string]string{"cpu": "total", "unit": "percent"},
            })
            if usage > 90 {
                emit(Message{
                    Source:  "cpu-monitor",
                    Type:    "alert",
                    Message: fmt.Sprintf("CPU usage %.1f%% exceeds threshold 90%%", usage),
                    Labels:  map[string]string{"severity": "critical"},
                })
            }
        }
    }
}
```

---

## 8. 完整示例（Python）

```python
#!/usr/bin/env python3
import json, sys, time, signal

SOURCE = "disk-monitor"
INTERVAL = 30  # seconds
THRESHOLD = 85  # percent

running = True

def emit(**kwargs):
    msg = {"source": SOURCE, "timestamp": int(time.time()), **kwargs}
    print(json.dumps(msg), flush=True)

def disk_usage(path="/"):
    import shutil
    total, used, _ = shutil.disk_usage(path)
    return used / total * 100

def handle_stop(sig, frame):
    global running
    running = False
    emit(type="event", message="plugin stopped gracefully")
    sys.exit(0)

signal.signal(signal.SIGTERM, handle_stop)
signal.signal(signal.SIGINT, handle_stop)

while running:
    try:
        usage = disk_usage("/")
        emit(type="metric", value=round(usage, 2),
             labels={"mount": "/", "unit": "percent"})
        if usage > THRESHOLD:
            emit(type="alert",
                 message=f"Disk / usage {usage:.1f}% exceeds {THRESHOLD}%",
                 labels={"severity": "warning", "mount": "/"})
    except Exception as e:
        emit(type="error", message=str(e))
    time.sleep(INTERVAL)
```

---

## 9. 完整示例（Shell）

```bash
#!/bin/bash
SOURCE="ping-monitor"
TARGET="${PING_TARGET:-8.8.8.8}"
INTERVAL=60

emit() {
    local type="$1" message="$2" value="$3"
    local ts
    ts=$(date +%s)
    if [ -n "$value" ]; then
        echo "{\"source\":\"$SOURCE\",\"type\":\"$type\",\"timestamp\":$ts,\"value\":$value,\"labels\":{\"target\":\"$TARGET\"}}"
    else
        echo "{\"source\":\"$SOURCE\",\"type\":\"$type\",\"timestamp\":$ts,\"message\":\"$message\",\"labels\":{\"target\":\"$TARGET\"}}"
    fi
}

trap 'emit event "plugin stopped"; exit 0' TERM INT

while true; do
    rtt=$(ping -c1 -W2 "$TARGET" 2>/dev/null | awk -F'/' 'END{print $5}')
    if [ -n "$rtt" ]; then
        emit metric "" "$rtt"
    else
        emit alert "Ping to $TARGET failed"
    fi
    sleep "$INTERVAL"
done
```

---

## 10. 打包与发布

### 1. 构建目录

```
cpu-monitor/
├── plugin.json
├── start.sh
├── start.cmd
└── cpu-monitor        # 已编译的二进制（针对目标平台）
```

### 2. 打包成 zip

```bash
# Linux/macOS
cd cpu-monitor
chmod +x start.sh cpu-monitor
zip -r ../cpu-monitor-linux-amd64.zip .

# 或保留顶级目录（两种方式 Agent 均支持）
zip -r ../cpu-monitor-linux-amd64.zip cpu-monitor/
```

### 3. 计算 SHA256

```bash
sha256sum cpu-monitor-linux-amd64.zip
# 或 macOS:
shasum -a 256 cpu-monitor-linux-amd64.zip
```

### 4. 多平台矩阵

| 文件名 | 目标环境 |
|--------|----------|
| `cpu-monitor-linux-amd64.zip` | Linux x86_64 |
| `cpu-monitor-linux-arm64.zip` | Linux ARM64（树莓派等） |
| `cpu-monitor-darwin-amd64.zip` | macOS Intel |
| `cpu-monitor-darwin-arm64.zip` | macOS Apple Silicon |
| `cpu-monitor-windows-amd64.zip` | Windows x86_64 |

---

## 11. 仓库源 index.json 格式

在 Git 仓库根目录放置 `index.json`，Manager 会定期拉取并同步到插件市场。

```json
{
  "version": "1",
  "updated_at": "2026-03-14T00:00:00Z",
  "plugins": [
    {
      "name": "cpu-monitor",
      "version": "1.2.0",
      "display_name": "CPU Monitor",
      "description": "Monitor CPU usage per core, push metrics every 10s, alert above threshold",
      "author": "your-org",
      "logo_url": "https://raw.githubusercontent.com/your-org/plugins/main/plugins/cpu-monitor/logo.png",
      "download_url": "https://github.com/your-org/plugins/releases/download/cpu-monitor-v1.2.0/cpu-monitor-linux-amd64.zip",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "os": ["linux", "darwin"],
      "arch": ["amd64", "arm64"],
      "tags": ["monitoring", "cpu", "system"],
      "message_types": ["metric", "alert"],
      "skills": [],
      "min_agent_version": "0.1.0",
      "homepage": "https://github.com/your-org/plugins"
    }
  ]
}
```

### 支持的仓库托管平台

| 平台 | 填写的仓库 URL | 自动解析为 |
|------|---------------|------------|
| GitHub | `https://github.com/org/repo` | `https://raw.githubusercontent.com/org/repo/main/index.json` |
| GitLab | `https://gitlab.com/org/repo` | `https://gitlab.com/org/repo/-/raw/main/index.json` |
| Gitea | `https://gitea.example.com/org/repo` | `https://gitea.example.com/org/repo/raw/branch/main/index.json` |
| 直接 URL | 以 `.json` 结尾的任意 URL | 直接请求 |

私有仓库在 Manager 源管理页面填写 **Auth Token**（GitHub Personal Access Token、GitLab Project Access Token 等）。

---

## 12. 调试技巧

### 本地测试消息输出

直接运行插件，观察 stdout 输出是否符合协议：

```bash
./start.sh | head -20 | python3 -m json.tool
```

### 验证 plugin.json

```bash
cat plugin.json | python3 -m json.tool
```

### 手动安装测试

将 zip 包上传到目标节点，或通过 Agent API 安装：

```bash
# 解压到 Agent 的 plugins 目录（默认 /var/lib/openmanager/plugins/）
unzip cpu-monitor-linux-amd64.zip -d /var/lib/openmanager/plugins/cpu-monitor/
chmod +x /var/lib/openmanager/plugins/cpu-monitor/start.sh
chmod +x /var/lib/openmanager/plugins/cpu-monitor/cpu-monitor

# 重启 Agent 触发插件加载
systemctl restart om-agent

# 查看 Agent 日志
journalctl -u om-agent -f
```

### 常见问题

| 现象 | 原因 | 解决 |
|------|------|------|
| 插件状态 `failed` | start.sh 不可执行或路径错误 | `chmod +x start.sh` |
| stdout 无输出 | 输出缓冲未刷新 | 使用 `flush=True` 或行缓冲模式 |
| JSON 解析错误 | 混入了非 JSON 内容（调试打印等） | 确保 stdout 只有 JSON 行 |
| 插件反复重启 | 进程立即退出 | 检查 start.sh 是否用了 `exec`，或添加循环 |
| metric 不在图表中 | labels 格式错误或 value 为 null | 确保 `value` 为 float64，不是字符串 |

---

*更多信息请参考：`requirements.md`（系统需求）、`schema.sql`（数据库结构）、`devplan.md`（开发计划）*
