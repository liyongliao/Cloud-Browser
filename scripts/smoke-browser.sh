#!/usr/bin/env bash
set -euo pipefail
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Run on the target native x86_64 Linux server; emulation cannot validate Chrome sandboxing.' >&2; exit 1; }
[[ $EUID == 0 ]] || { echo 'Run as root after prepare-host.sh.' >&2; exit 1; }
name="cb-smoke-$$"
profile=$(mktemp -d /srv/cloud-browser/smoke.XXXXXX)
cleanup(){ docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$profile"; }
trap cleanup EXIT
chown 1000:1000 "$profile"
AGENT_TOKEN=$(openssl rand -hex 32)
export AGENT_TOKEN
docker run -d --name "$name" --hostname cloud-browser --network cloud-browser-egress \
 --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges:true \
 --security-opt seccomp=/etc/cloud-browser/chrome-seccomp.json --security-opt apparmor=cloud-browser \
 --sysctl net.ipv6.conf.all.disable_ipv6=1 --memory 2g --memory-swap 2g --cpus 2 --shm-size 512m --pids-limit 512 \
 --read-only --tmpfs /tmp:rw,nosuid,nodev,size=512m --mount "type=bind,src=$profile,dst=/home/browser" -e AGENT_TOKEN cloud-browser-browser:local >/dev/null
ready=0
for i in {1..60}; do
 if docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" http://127.0.0.1:8081/healthz' >/dev/null 2>&1; then ready=1; break; fi
 [[ $(docker inspect -f '{{.State.Running}}' "$name") == true ]] || break
 sleep 1
done
if [[ $ready == 0 ]]; then docker logs --tail 50 "$name"; tail -80 "$profile"/.vnc/*.log 2>/dev/null || true; echo 'Chrome/display health check failed.' >&2; exit 1; fi
docker exec "$name" sh -c 'curl -fsS -u "browser:$AGENT_TOKEN" http://127.0.0.1:6901/vnc.html' >/dev/null
code=$(docker exec "$name" sh -c 'curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8081/healthz')
[[ $code == 403 ]]
docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d "{\"operationId\":\"0123456789abcdef0123456789abcdef\",\"url\":\"https://example.com\"}" http://127.0.0.1:8081/open' | python3 -c 'import json,sys; assert json.load(sys.stdin)["state"]=="SUCCEEDED"'
# Network checks use the actual container network path, including hostname resolution.
for target in http://169.254.169.254/ http://172.30.0.1/ http://10.0.0.1/ 'http://[::1]/'; do
 if docker exec "$name" curl -fsS --max-time 2 "$target" >/dev/null 2>&1; then echo "Unexpected network access: $target" >&2; exit 1; fi
done
# Cookies are ultimately website-controlled; this verifies profile state survives restart.
docker exec "$name" sh -c 'printf profile-persisted > "$HOME/smoke-marker"'
docker stop -t 30 "$name" >/dev/null
[[ $(docker inspect -f '{{.State.ExitCode}}' "$name") != 137 ]]
docker start "$name" >/dev/null
[[ $(docker exec "$name" cat /home/browser/smoke-marker) == profile-persisted ]]
echo 'Native browser smoke checks passed. Run the Web acceptance suite and manual media/IME checks next.'
