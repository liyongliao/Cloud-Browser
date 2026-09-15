#!/bin/bash
set -euo pipefail
umask 077
: "${AGENT_TOKEN:?required}"
mkdir -p "$HOME/.vnc" "$HOME/Uploads" "$HOME/Downloads" "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
if [ ! -f "$HOME/.vnc/tls.key" ] || [ ! -f "$HOME/.vnc/tls.crt" ]; then
  openssl req -x509 -nodes -newkey rsa:2048 -keyout "$HOME/.vnc/tls.key" -out "$HOME/.vnc/tls.crt" -days 365 -subj /CN=cloud-browser >/dev/null 2>&1
fi
cp /opt/cloud-browser/kasmvnc.yaml "$HOME/.vnc/kasmvnc.yaml"
printf '%s\n%s\n' "$AGENT_TOKEN" "$AGENT_TOKEN" | vncpasswd -u browser -w -r
# Explicit desktop selection avoids first-run prompts.
touch "$HOME/.vnc/.de-was-selected"
export VNC_DESKTOP_COMMAND=/opt/cloud-browser/desktop.sh
vncserver :1 -fg -xstartup /opt/cloud-browser/desktop.sh &
vnc_pid=$!
browser-agent &
agent_pid=$!
shutdown() {
  trap - TERM INT
  kill -TERM "$agent_pid" 2>/dev/null || true
  wait "$agent_pid" 2>/dev/null || true
  vncserver -kill :1 >/dev/null 2>&1 || true
  kill -TERM "$vnc_pid" 2>/dev/null || true
  wait "$vnc_pid" 2>/dev/null || true
}
trap shutdown TERM INT
status=0
wait -n "$agent_pid" "$vnc_pid" || status=$?
shutdown
exit "$status"
