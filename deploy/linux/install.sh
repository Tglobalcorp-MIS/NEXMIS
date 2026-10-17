#!/usr/bin/env bash
# Installs nexmis-server from a release directory on a systemd host.
#   sudo ./install.sh /path/to/dist
set -euo pipefail
DIST=${1:-dist}
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac

[ "$(id -u)" = 0 ] || { echo "請以 root 執行"; exit 1; }
id nexmis >/dev/null 2>&1 || useradd --system --home /var/lib/nexmis --shell /usr/sbin/nologin nexmis

install -m 0755 "$DIST/nexmis-server-linux-$ARCH" /usr/local/bin/nexmis-server
install -d -m 0700 -o nexmis -g nexmis /var/lib/nexmis /var/lib/nexmis/agent
cp -r "$DIST/agent/." /var/lib/nexmis/agent/
chown -R nexmis:nexmis /var/lib/nexmis/agent
install -d -m 0750 -o root -g nexmis /etc/nexmis
[ -f /etc/nexmis/nexmis.env ] || install -m 0640 -o root -g nexmis "$(dirname "$0")/nexmis.env.example" /etc/nexmis/nexmis.env
install -m 0644 "$(dirname "$0")/nexmis-server.service" /etc/systemd/system/nexmis-server.service
systemctl daemon-reload
echo "完成。請編輯 /etc/nexmis/nexmis.env 後執行：systemctl enable --now nexmis-server"
