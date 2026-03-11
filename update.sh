#!/bin/bash
# update.sh — build new cc-connect binary and hot-swap the running daemon
# Downtime: ~2-3 seconds (stop + swap + start)

set -e

BINARY="/home/ubuntu/.npm-global/lib/node_modules/cc-connect/bin/cc-connect"
DAEMON_BINARY="/home/ubuntu/.npm-global/lib/node_modules/cc-connect/run.js"
SRC_DIR="/home/ubuntu/codebase/cc-connect"
TMP_BINARY="/tmp/cc-connect-new"
VERSION="1.2.1"

echo "==> Building..."
cd "$SRC_DIR"
go build \
  -ldflags "-X main.version=${VERSION} -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo custom) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o "$TMP_BINARY" \
  ./cmd/cc-connect/

echo "==> Build OK ($(du -sh $TMP_BINARY | cut -f1))"

echo "==> Swapping binaries..."
cp --remove-destination "$TMP_BINARY" "$BINARY"
chmod +x "$BINARY"
cp --remove-destination "$TMP_BINARY" "$DAEMON_BINARY"
chmod +x "$DAEMON_BINARY"

# Kill the daemon PID directly — systemd sees it as a crash and auto-restarts
# via Restart=always/RestartSec=2. Using `systemctl stop` would tell systemd
# "don't restart", which is the wrong behavior here.
PID=$(systemctl --user show cc-connect -p MainPID --value)
echo "==> Killing daemon (PID $PID, will auto-restart in ~2s)..."
kill "$PID"

echo "==> Done. Daemon will restart in ~2 seconds."
