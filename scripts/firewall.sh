#!/usr/bin/env bash
set -euo pipefail
# Only the dedicated browser bridge is affected. Never flush unrelated host rules.
if ! docker network inspect cloud-browser-egress >/dev/null 2>&1; then
 docker network create --driver bridge --subnet 172.30.0.0/24 \
  --opt com.docker.network.bridge.name=cb-egress \
  --opt com.docker.network.bridge.enable_icc=false \
  --label cloud-browser.network=true cloud-browser-egress >/dev/null
fi
[[ $(docker network inspect -f '{{(index .IPAM.Config 0).Subnet}}' cloud-browser-egress) == '172.30.0.0/24' ]]
[[ $(docker network inspect -f '{{index .Options "com.docker.network.bridge.name"}}' cloud-browser-egress) == cb-egress ]]
[[ $(docker network inspect -f '{{index .Options "com.docker.network.bridge.enable_icc"}}' cloud-browser-egress) == false ]]
[[ $(docker network inspect -f '{{.EnableIPv6}}' cloud-browser-egress) == false ]]

# A deny guard stays at the top while the specific chain is updated.
iptables -w -I DOCKER-USER 1 -i cb-egress -j DROP
iptables -w -N CB-EGRESS 2>/dev/null || true
iptables -w -F CB-EGRESS
iptables -w -A CB-EGRESS -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
for cidr in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.168.0.0/16 198.18.0.0/15 224.0.0.0/4 240.0.0.0/4; do
 iptables -w -A CB-EGRESS -d "$cidr" -j REJECT
done
# Prevent access to services DNATed through this server's own public addresses.
while read -r address; do iptables -w -A CB-EGRESS -m conntrack --ctorigdst "$address" -j REJECT; done < <(ip -4 -o addr show scope global | awk '{split($4,a,"/");print a[1]}')
# Cloud providers may NAT the public IP outside the host interfaces.
# Supply those IPv4 addresses one per line; never infer them via an untrusted web service.
if [[ -f /etc/cloud-browser/public-ipv4 ]]; then
 while read -r address; do
  [[ -z "$address" || "$address" == \#* ]] && continue
  [[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { echo 'Invalid public IPv4 address' >&2; exit 1; }
  iptables -w -A CB-EGRESS -d "$address" -j REJECT
 done < /etc/cloud-browser/public-ipv4
fi
iptables -w -A CB-EGRESS -j RETURN
iptables -w -C DOCKER-USER -i cb-egress -j CB-EGRESS 2>/dev/null || iptables -w -A DOCKER-USER -i cb-egress -j CB-EGRESS
# Insert ahead of Docker's terminal RETURN, never append after it.
while iptables -w -C DOCKER-USER -i cb-egress -j CB-EGRESS 2>/dev/null; do iptables -w -D DOCKER-USER -i cb-egress -j CB-EGRESS; done
iptables -w -I DOCKER-USER 2 -i cb-egress -j CB-EGRESS
iptables -w -C INPUT -i cb-egress -m conntrack --ctstate NEW -j REJECT 2>/dev/null || iptables -w -I INPUT 1 -i cb-egress -m conntrack --ctstate NEW -j REJECT
ip6tables -w -C INPUT -i cb-egress -j REJECT 2>/dev/null || ip6tables -w -I INPUT 1 -i cb-egress -j REJECT
ip6tables -w -C FORWARD -i cb-egress -j REJECT 2>/dev/null || ip6tables -w -I FORWARD 1 -i cb-egress -j REJECT
while iptables -w -C DOCKER-USER -i cb-egress -j DROP 2>/dev/null; do iptables -w -D DOCKER-USER -i cb-egress -j DROP; done
