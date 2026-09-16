#!/usr/bin/env bash
set -euo pipefail

[[ $EUID == 0 ]] || { echo '请使用 sudo 运行安装脚本。' >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo '首版仅支持 x86_64 Linux。' >&2; exit 1; }
command -v curl >/dev/null || { echo '请先安装 curl。' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo '请先安装 coreutils。' >&2; exit 1; }

release=${CLOUD_BROWSER_VERSION:-latest}
base=https://github.com/liyongliao/Cloud-Browser/releases
if [[ $release == latest ]]; then
  base=$base/latest/download
else
  base=$base/download/$release
fi

work=$(mktemp -d)
cleanup(){ rm -rf "$work"; }
trap cleanup EXIT
curl -fL "$base/cloud-browser-linux-amd64" -o "$work/cloud-browser"
curl -fL "$base/cloud-browser-linux-amd64.sha256" -o "$work/cloud-browser-linux-amd64.sha256"
(cd "$work" && sha256sum -c cloud-browser-linux-amd64.sha256)
install -m 0755 "$work/cloud-browser" /usr/local/bin/cloud-browser

echo 'Cloud Browser 管理程序已安装到 /usr/local/bin/cloud-browser'
exec /usr/local/bin/cloud-browser setup
