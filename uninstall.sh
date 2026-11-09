#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
(( EUID == 0 )) || { echo 'Execute sudo ./uninstall.sh'; exit 1; }
echo 'Remove somente serviços LAB Router, IP registrado e tabelas com identificação própria. Dados cifrados e senha ficam preservados em /var/lib/lab-router.'
read -r -p 'Digite REMOVER LAB ROUTER: ' answer
[[ $answer == 'REMOVER LAB ROUTER' ]] || exit 0
for s in cwmp pppoe bras api helper ap dhcp; do systemctl disable --now "lab-router-$s.service" 2>/dev/null || true; done
# Simulated BRAS: own namespace, veth pair and /etc/netns overlay only.
ip link del pppoe-wan 2>/dev/null || true
ip netns del lab-bras 2>/dev/null || true
rm -rf -- /etc/netns/lab-bras
if [[ -f /var/lib/lab-router-network/config.json ]]; then
 cidr=$(python3 -c 'import json,ipaddress; c=json.load(open("/var/lib/lab-router-network/config.json")); print(ipaddress.ip_interface(c["lan"]["cidr"]))')
 ip addr del "$cidr" dev ap-tr069 2>/dev/null || true
fi
for family in inet ip; do
 table=lab_router; [[ $family == ip ]] && table=lab_router_nat
 if nft list table "$family" "$table" 2>/dev/null | rg -q 'Owned by LAB Router'; then nft delete table "$family" "$table"; fi
done
for s in api helper ap dhcp cwmp pppoe bras; do rm -f -- "/etc/systemd/system/lab-router-$s.service"; done
systemctl daemon-reload
rm -rf -- /usr/local/lib/lab-router /etc/lab-router /var/lib/lab-router-network /run/lab-router-helper
printf 'Removido. Dados/usuário preservados para recuperação; forwarding global e serviços preexistentes mantidos.\n'
