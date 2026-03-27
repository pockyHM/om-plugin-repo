#!/bin/bash
set -e
cd "$(dirname "$0")"
chmod +x om-node-monitor 2>/dev/null || true
exec ./om-node-monitor
