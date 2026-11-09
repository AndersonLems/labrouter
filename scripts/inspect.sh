#!/usr/bin/env bash
set -euo pipefail
printf '== Sistema ==\n'
cat /etc/os-release
id
printf '\n== Interfaces / rotas ==\n'
ip -br address
ip route
printf '\n== Wi-Fi ==\n'
iw dev
iw phy phy0 info
printf '\n== Serviços e processos ==\n'
ps -eo pid,user,args | rg '(^|/)(hostapd|dnsmasq)( |$)' || true
for unit in NetworkManager lab-router-api lab-router-helper lab-router-ap lab-router-dhcp lab-router-cwmp; do systemctl is-active "$unit" || true; done
printf '\n== Dependências ==\n'
for binary in go node npm hostapd hostapd_cli dnsmasq nft iw ip; do command -v "$binary" || true; done
printf '\n== Forwarding ==\n'
cat /proc/sys/net/ipv4/ip_forward
