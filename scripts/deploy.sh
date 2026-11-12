#!/usr/bin/env bash
# First-time server bootstrap for go-assistant.
# For routine code updates, use `make deploy` or `make deploy-yuri` instead.
set -euo pipefail

SERVER="${SERVER:-root@139.162.18.24}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/cryptoai_linode}"
REMOTE_DIR="${REMOTE_DIR:-/opt/assistant}"
SERVICE_NAME="${SERVICE_NAME:-assistant}"
DB_NAME="${DB_NAME:-assistant}"

cd "$(dirname "$0")/.."

echo "Building Linux binary (with embedded dashboard & migrations)..."
make build-linux

echo "Preparing remote directory $REMOTE_DIR on $SERVER..."
ssh -i "$SSH_KEY" "$SERVER" "mkdir -p '$REMOTE_DIR/files' '$REMOTE_DIR/backups'"

echo "Uploading binary..."
scp -i "$SSH_KEY" bin/assistant-linux "$SERVER:$REMOTE_DIR/assistant.new"

echo "Bootstrapping PostgreSQL database and systemd unit (non-destructive)..."
ssh -i "$SSH_KEY" "$SERVER" \
  "REMOTE_DIR='$REMOTE_DIR' SERVICE_NAME='$SERVICE_NAME' DB_NAME='$DB_NAME' bash -s" << 'REMOTE'
set -euo pipefail

chmod 755 "$REMOTE_DIR/assistant.new"
mv -f "$REMOTE_DIR/assistant.new" "$REMOTE_DIR/assistant"

su - postgres -c "psql -lqt | cut -d \| -f 1 | grep -qw '$DB_NAME' || createdb '$DB_NAME'"
su - postgres -c "psql -d '$DB_NAME' -c 'CREATE EXTENSION IF NOT EXISTS vector;'" >/dev/null

UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
if [ ! -f "$UNIT_PATH" ]; then
  cat > "$UNIT_PATH" << EOF
[Unit]
Description=Go Assistant (${SERVICE_NAME})
After=network.target postgresql.service

[Service]
Type=simple
ExecStart=${REMOTE_DIR}/assistant --config=${REMOTE_DIR}/config.yaml
WorkingDirectory=${REMOTE_DIR}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  echo "Created $UNIT_PATH"
else
  echo "Unit $UNIT_PATH already exists; leaving untouched."
fi
REMOTE

echo "Bootstrap complete. Ensure $REMOTE_DIR/config.yaml exists (chmod 600), then run:"
echo "  ssh -i $SSH_KEY $SERVER 'systemctl enable --now $SERVICE_NAME'"
