#!/usr/bin/env bash
# Deploy go-assistant to Linode server
set -euo pipefail

SERVER="root@172.104.56.5"
SSH_KEY="$HOME/.ssh/cryptoai_linode"
REMOTE_DIR="/opt/assistant"

echo "Building..."
cd "$(dirname "$0")/.."
make build-go

echo "Creating remote directory..."
ssh -i "$SSH_KEY" "$SERVER" "mkdir -p $REMOTE_DIR"

echo "Uploading binary..."
scp -i "$SSH_KEY" bin/assistant "$SERVER:$REMOTE_DIR/assistant"

echo "Uploading configs..."
scp -i "$SSH_KEY" configs/owner-profile.md "$SERVER:$REMOTE_DIR/owner-profile.md"
scp -i "$SSH_KEY" migrations/*.sql "$SERVER:$REMOTE_DIR/"

echo "Setting up systemd service..."
ssh -i "$SSH_KEY" "$SERVER" << 'REMOTE'
# Create assistant database if not exists
su - postgres -c "psql -lqt | grep -q assistant || createdb assistant"
su - postgres -c "psql -d assistant -c 'CREATE EXTENSION IF NOT EXISTS vector;'" 2>/dev/null || true

# Apply migrations
mkdir -p /opt/assistant/migrations
mv /opt/assistant/*.sql /opt/assistant/migrations/ 2>/dev/null || true

# Create systemd service
cat > /etc/systemd/system/assistant.service << 'EOF'
[Unit]
Description=Go Assistant
After=network.target postgresql.service

[Service]
Type=simple
ExecStart=/opt/assistant/assistant --config=/opt/assistant/config.yaml
WorkingDirectory=/opt/assistant
Restart=always
RestartSec=5
EnvironmentFile=/etc/assistant.env

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
echo "Service installed. Configure /etc/assistant.env and /opt/assistant/config.yaml, then: systemctl enable --now assistant"
REMOTE

echo "Done! Next steps:"
echo "1. SSH to server: ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5"
echo "2. Create /etc/assistant.env with tokens"
echo "3. Create /opt/assistant/config.yaml"
echo "4. systemctl enable --now assistant"
