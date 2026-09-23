#!/bin/bash
# Run on the VM only after provisioning the same random token in GitHub Actions.
set -euo pipefail
[[ $EUID == 0 && ( $# == 0 || ( $# == 1 && $1 == --hourly ) ) ]] || { echo 'Usage: sudo bash scripts/deploy/install-notify.sh [--hourly]' >&2; exit 2; }
token=/etc/wallet-demo-notify/token
[[ -f "$token" && ! -L "$token" ]] || { echo 'Provision /etc/wallet-demo-notify/token first' >&2; exit 1; }
[[ $(stat -c '%u:%g:%a' "$token") == '0:0:600' ]] || { echo 'Notification token must be root:root 0600' >&2; exit 1; }
[[ $(stat -c '%u:%g:%a' "$(dirname "$token")") == '0:0:700' ]] || { echo 'Notification token directory must be root:root 0700' >&2; exit 1; }
python3 - "$token" <<'PY'
import re
import sys
from pathlib import Path
if not re.fullmatch(rb'[a-f0-9]{64}\n?', Path(sys.argv[1]).read_bytes()):
    raise SystemExit('Expected 32 random bytes encoded as lowercase hex')
PY
[[ -x /usr/local/sbin/wallet-demo-poll ]] || { echo 'Install the verified poll script first' >&2; exit 1; }
root=$(cd "$(dirname "$0")/../.." && pwd)
install -o root -g root -m 0755 "$root/scripts/deploy/poll.sh" /usr/local/sbin/wallet-demo-poll
install -d -o root -g root -m 0755 /usr/local/libexec
install -o root -g root -m 0755 "$root/scripts/deploy/notify-server.py" /usr/local/libexec/wallet-demo-notify.py
for name in wallet-demo-notify.service wallet-demo-notify.path wallet-demo-notify-dispatch.service wallet-demo-update.service; do
  install -o root -g root -m 0644 "$root/scripts/deploy/$name" "/etc/systemd/system/$name"
done
if [[ ${1:-} == --hourly ]]; then
  install -o root -g root -m 0644 "$root/scripts/deploy/wallet-demo-update-hourly.timer" /etc/systemd/system/wallet-demo-update.timer
fi
systemctl daemon-reload
systemctl enable --now wallet-demo-notify.service
systemctl enable --now wallet-demo-notify.path
if [[ ${1:-} == --hourly ]]; then
  systemctl try-restart wallet-demo-update.timer
  echo 'Hourly fallback installed; verify the active timer and notification path.'
else
  echo 'Notification receiver installed; keep the existing timer until CI notification is verified.'
fi
