#!/usr/bin/env bash
# Build/preflight never change the network. Explicit interactive authorization is mandatory.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
if (( EUID != 0 )); then echo 'Execute sudo ./install.sh (o script pede confirmação antes de instalar/ativar).'; exit 1; fi
[[ -f /etc/os-release ]] && . /etc/os-release
[[ ${ID:-} == ubuntu && ${VERSION_ID:-} == 24.04 ]] || { echo 'Versão não validada. Requer Ubuntu 24.04.'; exit 1; }
./scripts/inspect.sh
[[ -x bin/lab-router ]] || { echo 'Compile antes como usuário normal: make build'; exit 1; }
if ps -eo args | awk '$1 ~ /(^|\/)hostapd$/ {found=1} END {exit !found}'; then
 if ! systemctl is-active --quiet lab-router-ap.service; then echo 'hostapd não gerenciado detectado: migre manualmente antes de instalar. Nenhum processo foi parado.'; exit 1; fi
fi
bin/lab-router validate
if nft list table inet lab_router &>/dev/null && ! nft list table inet lab_router | rg -q 'Owned by LAB Router'; then echo 'Tabela inet lab_router já existe sem identificação.'; exit 1; fi
if nft list table ip lab_router_nat &>/dev/null && ! nft list table ip lab_router_nat | rg -q 'Owned by LAB Router'; then echo 'Tabela NAT já existe sem identificação.'; exit 1; fi
if nft -j list ruleset | python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if any(x.get("chain",{}).get("hook")=="forward" and x["chain"].get("policy")=="drop" and x["chain"].get("table")!="lab_router" for x in d["nftables"]) else 1)'; then echo 'Política forward drop preexistente. Integração de firewall precisa ser revisada; instalação recusada.'; exit 1; fi
[[ $(cat /proc/sys/net/ipv4/ip_forward) == 1 ]] || { echo 'IPv4 forwarding global está 0. Habilitação precisa ser revisada com os serviços Docker/VPN; nenhuma mudança realizada.'; exit 1; }
cat <<'PROPOSAL'
Mudanças propostas:
  - Criar usuário de serviço lab-router, binário e 5 unidades lab-router-*.
  - API/CWMP sem root. API somente http://127.0.0.1:8088.
  - Helper root, apenas ap-tr069, sem sudo genérico para o backend.
  - IP do AP 192.168.50.1/24; DHCP .100–.200 / 12h, senha Wi-Fi aleatória.
  - Somente tabelas inet lab_router e ip lab_router_nat.
  - Bloquear clientes → host, destinos privados/corporativos e tun0.
  - Forwarding já está habilitado; nenhuma alteração global de sysctl.
  - Iniciar API/helper/CWMP e aplicar a rede; CWMP permanece desativado até configurado.
  - Não alterar wlp2s0, NetworkManager, tabelas Docker/LXC/VPN nem /etc/nftables.conf.
  - A persistência após boot será perguntada separadamente.
PROPOSAL
read -r -p 'Digite ATIVAR LAB ROUTER para autorizar estas mudanças: ' answer
[[ $answer == 'ATIVAR LAB ROUTER' ]] || { echo 'Cancelado sem alterações.'; exit 0; }
missing=()
for package in hostapd dnsmasq-base nftables iw iproute2 iputils-ping traceroute python3 ripgrep ppp pppoe; do
 [[ $(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$package")
done
if (( ${#missing[@]} )); then apt-get update; apt-get install -y "${missing[@]}"; fi
getent group lab-router >/dev/null || groupadd --system lab-router
id lab-router &>/dev/null || useradd --system --gid lab-router --home-dir /var/lib/lab-router --shell /usr/sbin/nologin lab-router
install -d -m 0755 /usr/local/lib/lab-router
install -d -m 0750 -o root -g lab-router /etc/lab-router
install -d -m 0700 -o lab-router -g lab-router /var/lib/lab-router
install -d -m 0700 -o root -g root /var/lib/lab-router-network
install -d -m 0750 -o lab-router -g lab-router /var/lib/lab-router-dhcp
install -m 0755 bin/lab-router /usr/local/lib/lab-router/lab-router
if [[ ! -f /etc/lab-router/environment ]]; then
 cat > /etc/lab-router/environment <<'ENV'
LAB_BIND=127.0.0.1:8088
LAB_DATA_DIR=/var/lib/lab-router
LAB_MODE=live
ENV
 chmod 0640 /etc/lab-router/environment
 chown root:lab-router /etc/lab-router/environment
fi
# Optional one-time migration. Stop the preview first; SQLite backup includes committed WAL.
if [[ -n ${LAB_IMPORT_DATA:-} && ! -f /var/lib/lab-router/lab-router.db ]]; then
 [[ $LAB_IMPORT_DATA == /* && -f $LAB_IMPORT_DATA/lab-router.db && -f $LAB_IMPORT_DATA/master.key ]] || { echo 'Diretório de importação inválido'; exit 1; }
 python3 - "$LAB_IMPORT_DATA" <<'PYIMPORT'
import pathlib,sqlite3,sys,shutil,os
src=pathlib.Path(sys.argv[1]); dst=pathlib.Path('/var/lib/lab-router')
a=sqlite3.connect((src/'lab-router.db').as_uri()+'?mode=ro',uri=True)
b=sqlite3.connect(dst/'lab-router.db'); a.backup(b); b.close(); a.close()
for name in ['master.key','initial-admin-password']:
 if (src/name).is_file(): shutil.copyfile(src/name,dst/name)
for path in dst.iterdir():
 if path.is_file(): os.chmod(path,0o600)
PYIMPORT
 chown -R lab-router:lab-router /var/lib/lab-router
fi
# Initialize exactly once, as the unprivileged service account.
if [[ ! -f /var/lib/lab-router/lab-router.db ]]; then runuser -u lab-router -- env LAB_DATA_DIR=/var/lib/lab-router /usr/local/lib/lab-router/lab-router init; fi
install -m 0644 deploy/lab-router-*.service /etc/systemd/system/
touch /var/lib/lab-router-network/authorized
systemctl daemon-reload
systemctl restart lab-router-helper.service lab-router-api.service lab-router-cwmp.service
# Apply encrypted stored configuration through the helper; no admin secret is printed.
runuser -u lab-router -- env LAB_DATA_DIR=/var/lib/lab-router /usr/local/lib/lab-router/lab-router apply-initial
read -r -p 'Habilitar API/helper/CWMP após reboot? Digite SIM: ' boot
if [[ $boot == SIM ]]; then systemctl enable lab-router-api.service lab-router-helper.service lab-router-cwmp.service; fi
echo 'Painel: http://127.0.0.1:8088'
echo 'Senha administrativa inicial: sudo cat /var/lib/lab-router/initial-admin-password'
echo 'Senha Wi-Fi: sudo /usr/local/lib/lab-router/lab-router show-wifi-password (exibe somente no terminal local).'
echo 'Nenhuma sessão com ACS foi iniciada automaticamente se CWMP permanecer desativado.'
