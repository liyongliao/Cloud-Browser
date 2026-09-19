#!/usr/bin/env bash
set -euo pipefail

[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || {
  echo 'Run on the target native x86_64 Linux server.' >&2
  exit 1
}
[[ $EUID == 0 ]] || { echo 'Run as root after prepare-host.sh.' >&2; exit 1; }

image=${BROWSER_IMAGE:-cloud-browser-browser:local}
name="cb-bridge-smoke-$$"
profile=$(mktemp -d /srv/cloud-browser/bridge-smoke.XXXXXX)
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  rm -rf "$profile"
}
trap cleanup EXIT
chown 1000:1000 "$profile"

cat >"$profile/bridge-server.py" <<'PY'
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

result = {"paste": "", "text": "", "file": ""}
page = """<!doctype html><meta charset=utf-8>
<input id=text autofocus><button id=choose type=button>选择文件</button>
<input id=file type=file multiple hidden>
<script>
let phase = 'paste';
text.oninput = () => {
  fetch('/report?' + phase + '=' + encodeURIComponent(text.value));
  if (phase === 'paste') {
    text.value = '';
    phase = 'text';
  } else {
    text.select();
  }
};
choose.onclick = () => file.click();
file.onchange = () => fetch('/report?file=' + encodeURIComponent(
  Array.from(file.files).map(item => item.name).join(',')));
</script>"""

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/report":
            for key, values in parse_qs(parsed.query).items():
                if key in result and values:
                    result[key] = values[-1]
            Path("/tmp/bridge-result.json").write_text(
                json.dumps(result, ensure_ascii=False), encoding="utf-8"
            )
            self.send_response(204)
            self.end_headers()
            return
        body = page.encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass

ThreadingHTTPServer(("0.0.0.0", 8082), Handler).serve_forever()
PY
chown 1000:1000 "$profile/bridge-server.py"

AGENT_TOKEN=$(openssl rand -hex 32)
export AGENT_TOKEN
docker run -d --name "$name" --hostname cloud-browser --network cloud-browser-egress \
  --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges:true \
  --security-opt seccomp=/etc/cloud-browser/chrome-seccomp.json --security-opt apparmor=cloud-browser \
  --sysctl net.ipv6.conf.all.disable_ipv6=1 --memory 2g --memory-swap 2g --cpus 2 \
  --shm-size 512m --pids-limit 512 --read-only --tmpfs /tmp:rw,nosuid,nodev,size=512m \
  --mount "type=bind,src=$profile,dst=/home/browser" -e AGENT_TOKEN "$image" >/dev/null

ready=0
for _ in {1..60}; do
  if docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" http://127.0.0.1:8081/healthz' >/dev/null 2>&1; then
    ready=1
    break
  fi
  [[ $(docker inspect -f '{{.State.Running}}' "$name") == true ]] || break
  sleep 1
done
if [[ $ready == 0 ]]; then
  docker logs --tail 80 "$name"
  echo 'Browser did not become healthy.' >&2
  exit 1
fi

docker exec "$name" sh -c 'python3 "$HOME/bridge-server.py" >/tmp/bridge-server.log 2>&1 &'
docker exec "$name" sh -c 'printf bridge-file > "$HOME/Uploads/bridge-test.txt"'
docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d '\''{"operationId":"fedcba9876543210fedcba9876543210","url":"http://cloud-browser:8082"}'\'' http://127.0.0.1:8081/open' >/dev/null
sleep 2

docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d '\''{"action":"paste","text":"剪贴板粘贴"}'\'' http://127.0.0.1:8081/input' >/dev/null
docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d '\''{"action":"insert","text":"中文桥接输入"}'\'' http://127.0.0.1:8081/input' >/dev/null
copied=$(docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d '\''{"action":"copy"}'\'' http://127.0.0.1:8081/input' | python3 -c 'import json,sys; print(json.load(sys.stdin)["text"])')
[[ $copied == '中文桥接输入' ]] || { echo 'Remote clipboard copy did not return selected text.' >&2; exit 1; }

docker exec "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" http://127.0.0.1:8081/file-chooser > /tmp/bridge-chooser.json' &
chooser_pid=$!
sleep 1
docker exec "$name" sh -c 'DISPLAY=:1 xdotool key Tab Return'
wait "$chooser_pid"

chooser=$(docker exec "$name" python3 -c 'import json; print(json.load(open("/tmp/bridge-chooser.json"))["id"])')
docker exec -e CHOOSER="$chooser" "$name" sh -c 'curl -fsS -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d '\''{"names":["bridge-test.txt"]}'\'' "http://127.0.0.1:8081/file-chooser/$CHOOSER"' >/dev/null

matched=0
for _ in {1..30}; do
  if docker exec "$name" python3 -c 'import json; value=json.load(open("/tmp/bridge-result.json")); assert value == {"paste":"剪贴板粘贴","text":"中文桥接输入","file":"bridge-test.txt"}' 2>/dev/null; then
    matched=1
    break
  fi
  sleep 0.2
done
if [[ $matched == 0 ]]; then
  docker exec "$name" sh -c 'cat /tmp/bridge-result.json 2>/dev/null || true'
  echo 'Native input or file chooser bridge did not reach the page.' >&2
  exit 1
fi

echo 'Native IME, clipboard and remote file chooser checks passed.'
