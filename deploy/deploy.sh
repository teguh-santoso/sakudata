#!/usr/bin/env sh
# Build and deploy SakuData to a remote host over SSH.
#
# Required environment (never hard-code secrets in this file):
#   SAKUDATA_HOST   remote host/IP
#   SAKUDATA_USER   ssh user (default: root)
#   SAKUDATA_PATH   install dir on the server (default: /opt/sakudata)
# Optional:
#   SAKUDATA_SSH_KEY  path to a private key (defaults to your ssh agent/config)
#   SAKUDATA_SERVICE  systemd unit name (default: sakudata)
#
# Example:
#   SAKUDATA_HOST=2001:db8::1 SAKUDATA_USER=root ./deploy/deploy.sh
set -eu

HOST="${SAKUDATA_HOST:?set SAKUDATA_HOST}"
USER="${SAKUDATA_USER:-root}"
DIR="${SAKUDATA_PATH:-/opt/sakudata}"
SERVICE="${SAKUDATA_SERVICE:-sakudata}"
SSH_OPTS="-o StrictHostKeyChecking=accept-new"
[ -n "${SAKUDATA_SSH_KEY:-}" ] && SSH_OPTS="$SSH_OPTS -i $SAKUDATA_SSH_KEY"

echo "==> building linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o sakudata-linux-amd64 .

echo "==> uploading to $USER@$HOST:$DIR"
ssh $SSH_OPTS "$USER@$HOST" "mkdir -p '$DIR'"
scp $SSH_OPTS sakudata-linux-amd64 "$USER@$HOST:$DIR/sakudata.new"

echo "==> activating"
ssh $SSH_OPTS "$USER@$HOST" "
  set -e
  cd '$DIR'
  if [ -f sakudata ]; then cp sakudata sakudata.prev; fi
  mv sakudata.new sakudata
  chmod +x sakudata
  if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/$SERVICE.service ]; then
    systemctl restart '$SERVICE'
    systemctl --no-pager --lines=5 status '$SERVICE' || true
  else
    echo 'systemd unit $SERVICE not installed; start the binary manually.'
  fi
"
rm -f sakudata-linux-amd64
echo "==> done. Verify: curl -fsS https://<your-domain>/api/health"
