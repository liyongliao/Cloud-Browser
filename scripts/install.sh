#!/usr/bin/env bash
set -euo pipefail

[[ $EUID == 0 ]] || { echo '请使用 sudo ./scripts/install.sh 运行。' >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo '首次安装目前支持 x86_64 Linux。' >&2; exit 1; }

install_root=$(cd "$(dirname "$0")/.." && pwd)
if [[ -f "$install_root/.setup-complete" ]]; then
  echo 'Cloud Browser 已完成初始化。请登录网页管理，不要重复运行首次安装。' >&2
  exit 1
fi

if ! command -v apt-get >/dev/null; then
  echo '自动依赖安装目前支持 Debian/Ubuntu；请先安装 Docker、Compose、AppArmor、iptables 和 OpenSSL。' >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y docker.io docker-compose apparmor apparmor-utils iptables openssl ca-certificates curl
systemctl enable --now docker

"$install_root/scripts/prepare-host.sh"
docker build --target setup -t cloud-browser-setup:local -f "$install_root/deploy/Dockerfile" "$install_root"

setup_token=$(openssl rand -hex 32)
setup_network=cloud-browser-setup
docker network inspect "$setup_network" >/dev/null 2>&1 || docker network create "$setup_network" >/dev/null
docker rm -f cloud-browser-setup >/dev/null 2>&1 || true
docker run --rm -d \
  --name cloud-browser-setup \
  --network "$setup_network" \
  -p 0.0.0.0:8090:8090 \
  -e SETUP_TOKEN="$setup_token" \
  -e SETUP_ROOT="$install_root" \
  -e SETUP_CONTAINER=cloud-browser-setup \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /etc/cloud-browser:/etc/cloud-browser \
  -v "$install_root:$install_root" \
  -w "$install_root" \
  cloud-browser-setup:local >/dev/null

setup_address=${SETUP_ADDRESS:-$(hostname -I | awk '{print $1}')}
install -d -m 700 /etc/cloud-browser
printf '%s\n' "$setup_token" > /etc/cloud-browser/setup-token
chmod 600 /etc/cloud-browser/setup-token

echo
echo 'Cloud Browser 安装向导已启动。'
echo "请在浏览器打开：http://${setup_address}:8090/#token=${setup_token}"
echo
echo '若 8090 端口不对公网开放，可在本机运行：'
echo 'ssh -L 8090:127.0.0.1:8090 root@你的服务器'
echo "然后打开：http://127.0.0.1:8090/#token=${setup_token}"
