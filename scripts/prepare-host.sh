#!/usr/bin/env bash
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run as root on the target Linux server.' >&2; exit 1; }
[[ $(uname -m) == x86_64 && $(uname -s) == Linux ]] || { echo 'Requires x86_64 Linux.' >&2; exit 1; }
command -v docker >/dev/null
command -v apparmor_parser >/dev/null
command -v iptables >/dev/null
command -v ip6tables >/dev/null
[[ $(cat /proc/sys/user/max_user_namespaces) -gt 0 ]] || { echo 'Enable user namespaces for the Chrome sandbox before deployment.' >&2; exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
install -d -m 700 /srv/cloud-browser/profiles
install -d -m 755 /etc/cloud-browser
install -m 644 "$root/deploy/browser/apparmor.profile" /etc/apparmor.d/cloud-browser
apparmor_parser -r /etc/apparmor.d/cloud-browser
install -m 644 "$root/deploy/browser/chrome-seccomp.json" /etc/cloud-browser/chrome-seccomp.json
install -m 755 "$root/scripts/firewall.sh" /etc/cloud-browser/firewall.sh
cat > /etc/systemd/system/cloud-browser-firewall.service <<'UNIT'
[Unit]
Description=Cloud Browser network isolation
After=docker.service
Requires=docker.service
PartOf=docker.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/etc/cloud-browser/firewall.sh
[Install]
WantedBy=docker.service
UNIT
systemctl daemon-reload
systemctl enable --now cloud-browser-firewall.service
echo 'Host isolation prepared. Build the browser image before starting the Compose stack.'
