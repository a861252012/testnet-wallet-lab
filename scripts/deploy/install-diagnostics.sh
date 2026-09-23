#!/bin/bash
# Install only host diagnostics; does not restart the wallet or tunnel.
set -euo pipefail
[[ $EUID == 0 && $# == 0 ]] || { echo 'Usage: sudo bash scripts/deploy/install-diagnostics.sh' >&2; exit 2; }
root=$(cd "$(dirname "$0")" && pwd)
install -o root -g root -m 0755 "$root/record-health.py" /usr/local/sbin/wallet-demo-record-health
install -o root -g root -m 0644 "$root/wallet-demo-health.service" /etc/systemd/system/
install -o root -g root -m 0644 "$root/wallet-demo-health.timer" /etc/systemd/system/
install -d -o root -g root -m 0755 /etc/systemd/journald.conf.d
install -o root -g root -m 0644 "$root/wallet-demo-journal.conf" /etc/systemd/journald.conf.d/
# Keep previous-boot kernel/tunnel records without unlimited disk growth.
mkdir -p /var/log/journal
systemd-tmpfiles --create --prefix /var/log/journal
systemctl restart systemd-journald
journalctl --flush
systemctl daemon-reload
systemctl enable --now wallet-demo-health.timer
systemctl start wallet-demo-health.service
