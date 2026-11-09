# LAB Router

**CPE virtual em Linux para laboratório de TR-069.** Transforma um notebook Ubuntu num roteador doméstico gerenciável por ACS: ponto de acesso Wi-Fi real, DHCP, NAT/firewall, cliente CWMP nativo (TR-069 / modelo TR-098), Connection Request por HTTP, XMPP e STUN, speedtest TR-143, discador PPPoE com BRAS simulado e um painel web para operar e diagnosticar tudo.

O objetivo é testar provisionamento, monitoramento e diagnóstico de um ACS (GenieACS ou compatível) **sem precisar de um roteador físico**, e enxergando por dentro o que a CPE recebe, aplica e responde.

> ⚠️ Projeto de laboratório. A CPE altera serviços de rede do host (hostapd, dnsmasq, nftables). Leia [Segurança e isolamento](#segurança-e-isolamento) antes de instalar.

---

## Sumário

- [Visão geral](#visão-geral)
- [Conceitos](#conceitos)
- [Módulos](#módulos)
- [Requisitos](#requisitos)
- [Instalação pelo terminal](#instalação-pelo-terminal)
- [Configuração pelo terminal](#configuração-pelo-terminal)
- [Operação: serviços, logs e atualização](#operação-serviços-logs-e-atualização)
- [Parâmetros TR-069 expostos](#parâmetros-tr-069-expostos)
- [Integração com o ACS (widgets)](#integração-com-o-acs-widgets)
- [Desenvolvimento e testes](#desenvolvimento-e-testes)
- [Segurança e isolamento](#segurança-e-isolamento)
- [Limitações conhecidas](#limitações-conhecidas)
- [Solução de problemas](#solução-de-problemas)
- [Documentação complementar](#documentação-complementar)

---

## Visão geral

```
                 Internet
                    │
        ┌───────────┴────────────┐
        │  Wi-Fi do escritório   │  ← WAN (wlp2s0, cliente) — nunca é alterada
        └───────────┬────────────┘
                    │
┌───────────────────┴───────────────────────────────────────────────┐
│ Notebook Ubuntu = CPE virtual                                     │
│                                                                   │
│  lab-router-api ── painel web + API REST (127.0.0.1:8088)         │
│  lab-router-cwmp ─ agente TR-069 ⇄ ACS (Inform, RPCs, XMPP, STUN) │
│  lab-router-helper (root) ─ aplica rede com rollback              │
│       ├─ lab-router-ap    hostapd  → AP "ap-tr069" (mesmo rádio)  │
│       ├─ lab-router-dhcp  dnsmasq  → 192.168.50.0/24              │
│       ├─ nftables         NAT + isolamento do laboratório         │
│       └─ lab-router-pppoe / lab-router-bras  PPPoE ⇄ BRAS (netns) │
└───────────────────┬───────────────────────────────────────────────┘
                    │ Wi-Fi do laboratório (SSID configurável)
            celulares / notebooks de teste
```

| Camada | Tecnologia |
|---|---|
| Backend | Go 1.22+, chi, SQLite (dados cifrados com chave mestra local) |
| Frontend | React 19 + TypeScript + Vite, **embutido no binário** (`go:embed`) |
| Tempo real | Server-Sent Events (snapshot a cada 3 s) |
| Rede | hostapd, dnsmasq, nftables, iw, iproute2, pppd + rp-pppoe |
| TR-069 | Cliente SOAP/CWMP próprio (sem dependência de agente externo) |

Um único executável (`lab-router`) roda em vários modos (`api`, `cwmp`, `helper`, …), cada um como serviço systemd com o mínimo de privilégio necessário.

---

## Conceitos

### CPE, ACS e sessão CWMP

- **CPE** (*Customer Premises Equipment*): o roteador do cliente, aqui o notebook.
- **ACS** (*Auto-Configuration Server*): o servidor que gerencia as CPEs (ex.: GenieACS).
- **CWMP / TR-069**: protocolo SOAP sobre HTTP entre os dois.

**Quem inicia é sempre a CPE.** Uma sessão funciona assim:

1. CPE → ACS: `Inform` (identidade, eventos, alguns parâmetros).
2. ACS → CPE: `InformResponse`.
3. CPE → ACS: POST vazio ("tem algo para mim?").
4. ACS → CPE: RPCs (`GetParameterValues`, `SetParameterValues`, …) até responder vazio (HTTP 204).

**Eventos do Inform** usados pelo projeto:

| Evento | Quando |
|---|---|
| `0 BOOTSTRAP` | Primeira conexão / após FactoryReset |
| `1 BOOT` | O serviço `lab-router-cwmp` iniciou |
| `2 PERIODIC` | A cada *Periodic Inform Interval* (mínimo 5 s neste projeto) |
| `4 VALUE CHANGE` | Configuração mudou localmente |
| `6 CONNECTION REQUEST` | O ACS pediu uma sessão |
| `8 DIAGNOSTICS COMPLETE` | Um diagnóstico TR-143 terminou |
| `M Reboot` | Reboot pedido pelo ACS concluído |

### Connection Request: como o ACS "acorda" a CPE

O ACS não manda comandos a qualquer momento; ele pede que a CPE abra uma sessão:

| Mecanismo | Como chega | Quando usar |
|---|---|---|
| **HTTP** | GET com Digest na URL da CPE (porta 7548) | CPE com IP alcançável |
| **XMPP** (TR-069 Anexo K) | Mensagem IQ por uma conexão TCP de saída sempre aberta (5222, STARTTLS + SASL) | CPE atrás de NAT |
| **STUN / UDP** (TR-111) | Pacote UDP assinado com HMAC-SHA1 para o mapeamento NAT descoberto por STUN | CPE atrás de NAT, sem XMPP |

As credenciais de Connection Request são próprias; **se ficarem vazias, valem o usuário/senha CWMP**.

### Modelos de dados

- **TR-098** (`InternetGatewayDevice.`): modelo usado por esta CPE.
- **TR-181** (`Device.`): modelo mais novo; não implementado.
- Parâmetros `X_LAB_*` são extensões próprias (ex.: sinal do cliente Wi-Fi, que o TR-098 não define).

### TR-143 (diagnósticos de desempenho)

`DownloadDiagnostics` / `UploadDiagnostics`: o ACS grava a URL e `DiagnosticsState=Requested`; **a CPE executa depois de encerrar a sessão**, envia `8 DIAGNOSTICS COMPLETE` e o ACS lê `ROMTime`, `BOMTime`, `EOMTime`, bytes e o estado final. Mede com **uma conexão TCP**, por isso o resultado depende muito da latência até o servidor.

### PPPoE e BRAS

- **PPPoE**: PPP sobre Ethernet — descoberta (PADI/PADO/PADR/PADS), autenticação (CHAP/PAP) e configuração IP (IPCP).
- **BRAS**: o concentrador do provedor que autentica e entrega IP.
- Aqui o BRAS é **simulado** num *network namespace* (`lab-bras`) ligado à CPE por um par **veth** (cabo virtual). A sessão é real, mas isolada: não vira rota padrão nem muda o DNS do notebook.

### STA+AP no mesmo rádio

O notebook usa **um único rádio** como cliente (WAN) e como ponto de acesso. O hardware só permite **um canal por vez**, então o AP fica **travado no canal da WAN**. Trocar a banda/canal do AP exigiria um segundo adaptador.

---

## Módulos

| Módulo | O que faz | Onde |
|---|---|---|
| **Painel web** | Dashboard em tempo real, configuração, diagnósticos; nenhuma tela mostra JSON cru | `web/src` |
| **API REST** | Autenticação por senha local, sessão com cookie + CSRF, SSE | `internal/api` |
| **Helper de rede** (root) | Gera configs, aplica com validação e **rollback**; recusa assumir serviços não gerenciados | `internal/network` |
| **Wi-Fi (AP)** | hostapd em `ap-tr069`: SSID, WPA2/WPA3, oculto, isolamento de clientes, bloqueio por MAC | `network/render.go` |
| **DHCP** | dnsmasq: pool, tempo de concessão, DNS entregues, reservas por MAC | `network/render.go` |
| **Firewall / NAT** | nftables próprio (`inet lab_router`, `ip lab_router_nat`), masquerade só da sub-rede do lab, redirecionamento de portas | `network/render.go` |
| **Agente CWMP** | Inform, Periodic Inform, GPV/SPV/GPN, Reboot, FactoryReset (desligado por padrão), faults detalhados | `internal/cwmp` |
| **XMPP / STUN** | Connection Request por XMPP (TLS/SASL) e UDP (STUN + HMAC) | `internal/traversal` |
| **TR-143** | Download/Upload Diagnostics via HTTP/HTTPS, eventos e resultados padronizados | `cwmp/tr143.go` |
| **Hosts e clientes** | `Hosts.Host` e `AssociatedDevice` com clientes reais + **hosts simulados** opcionais | `cwmp/hosts.go` |
| **Recursos e tráfego** | CPU, memória, contadores de bytes WAN/WLAN (64 bits) — também enviados em todo Inform | `cwmp/status.go` |
| **PPPoE + BRAS** | Discador `pppd` + `pppoe-server` em netns; status e erros TR-098 | `network/pppoe.go`, `cwmp/ppp.go` |
| **Diagnóstico de rede** | Ping, traceroute, DNS lookup e speedtest (Cloudflare, 4 conexões) com resultado visual | `system`, `web/src/views.tsx` |
| **Backup / auditoria** | Snapshots cifrados com restauração; log de auditoria e histórico de sessões CWMP | `internal/store` |

### Serviços systemd

| Unit | Usuário | Função |
|---|---|---|
| `lab-router-api` | `lab-router` | Painel e API (só `127.0.0.1:8088`); `CAP_NET_RAW` apenas para o ping |
| `lab-router-cwmp` | `lab-router` | Agente TR-069, XMPP, STUN, TR-143 |
| `lab-router-helper` | root (sandbox) | Única peça privilegiada; socket Unix local |
| `lab-router-ap` | root | hostapd |
| `lab-router-dhcp` | `lab-router` | dnsmasq |
| `lab-router-bras` | root | BRAS simulado (netns `lab-bras`) — só com PPPoE ligado |
| `lab-router-pppoe` | root | Discador `pppd` — só com PPPoE ligado |

---

## Requisitos

- **Ubuntu 24.04** (o instalador recusa outras versões).
- Placa Wi-Fi com suporte a **AP** e à combinação `managed + AP` (`iw list` → *valid interface combinations*).
- Para compilar: Go ≥ 1.22, GCC (`build-essential`, SQLite usa cgo), Node ≥ 20.19 e npm.
- Pacotes de execução (o instalador instala o que faltar): `hostapd dnsmasq-base nftables iw iproute2 iputils-ping traceroute python3 ripgrep ppp pppoe`.
- `net.ipv4.ip_forward=1` já habilitado (o instalador **não** muda sysctl global; ele recusa e explica).

---

## Instalação pelo terminal

```bash
# 1. Dependências de compilação
sudo apt install build-essential golang-go nodejs npm   # ou Go/Node mais novos pelos sites oficiais

# 2. Código
git clone https://github.com/AndersonLems/labrouter.git ~/labrouter
cd ~/labrouter

# 3. Compilar (frontend + binário) — como usuário normal
make build

# 4. Inspeção prévia (somente leitura)
./scripts/inspect.sh

# 5. Instalar e ativar (interativo)
sudo ./install.sh
```

O instalador:

1. Valida o ambiente e **recusa** se encontrar riscos (hostapd não gerenciado, firewall com `policy drop` em forward, tabelas homônimas, forwarding desligado).
2. Mostra as mudanças propostas e só continua se você digitar **`ATIVAR LAB ROUTER`**.
3. Cria o usuário `lab-router`, instala binário e units, gera a senha administrativa e a senha Wi-Fi aleatórias e aplica a rede.
4. Pergunta separadamente se deve **iniciar no boot** (digite `SIM`).

Depois da instalação:

```bash
# Painel
xdg-open http://127.0.0.1:8088

# Senha administrativa inicial
sudo cat /var/lib/lab-router/initial-admin-password

# Senha do Wi-Fi do laboratório (só no terminal local)
sudo /usr/local/lib/lab-router/lab-router show-wifi-password
```

### Desinstalar

```bash
sudo ./uninstall.sh      # confirme com: REMOVER LAB ROUTER
```

Remove serviços, IP do AP, tabelas nftables próprias, namespace/veth do BRAS. **Preserva** `/var/lib/lab-router` (banco cifrado, chave e senha) para recuperação.

---

## Configuração pelo terminal

### Variáveis de ambiente

Arquivo `/etc/lab-router/environment` (modelo em [`.env.example`](.env.example)):

| Variável | Padrão | Significado |
|---|---|---|
| `LAB_BIND` | `127.0.0.1:8088` | Endereço da API/painel. Mantenha em loopback: a API recusa requisições cujo `Host` não seja `127.0.0.1`, `localhost` ou `::1` |
| `LAB_DATA_DIR` | `/var/lib/lab-router` | Banco SQLite cifrado, chave mestra, senha inicial |
| `LAB_MODE` | `observation` | `observation`: só lê o sistema e recusa mudanças de rede. `live`: aplica de verdade |
| `LAB_ADMIN_PASSWORD` | — | Só na primeira inicialização; prefira `lab-router init` |

### Comandos do binário

```bash
B=/usr/local/lib/lab-router/lab-router

sudo -u lab-router env LAB_DATA_DIR=/var/lib/lab-router $B init         # cria banco, chave e senha (uma vez)
$B validate                                                             # valida o ambiente sem alterar nada
sudo -u lab-router env LAB_DATA_DIR=/var/lib/lab-router $B apply-initial # aplica a config salva via helper
sudo -u lab-router env LAB_DATA_DIR=/var/lib/lab-router $B inform-once   # uma sessão CWMP e sai
sudo $B show-wifi-password                                              # mostra a senha do AP
# Modos de serviço (usados pelo systemd): api | cwmp | helper
```

### API pelo terminal (curl)

Toda escrita exige sessão + cabeçalho `X-CSRF-Token`:

```bash
API=http://127.0.0.1:8088/api
JAR=$(mktemp)
PASS=$(sudo cat /var/lib/lab-router/initial-admin-password)
CSRF=$(curl -s -c $JAR -H 'Content-Type: application/json' \
       -d "{\"password\":\"$PASS\"}" $API/auth/login | python3 -c 'import sys,json;print(json.load(sys.stdin)["csrf"])')

get() { curl -s -b $JAR "$API/$1"; }
put() { curl -s -b $JAR -X PUT -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" -d "$2" "$API/$1"; }
post(){ curl -s -b $JAR -X POST -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" -d "${2:-{}}" "$API/$1"; }
```

Exemplos:

```bash
# Ler configuração (senhas sempre voltam vazias)
get tr069/config | python3 -m json.tool

# Apontar a CPE para o ACS (Periodic Inform de 60 s)
put tr069/config "$(get tr069/config | python3 -c '
import sys,json; c=json.load(sys.stdin)
c.update(enabled=True, url="http://acs.example.com:7547/", username="cpe", password="SENHA", periodic_enable=True, interval=60)
print(json.dumps(c))')"

# Wi-Fi do laboratório
put wifi "$(get wifi | python3 -c '
import sys,json; c=json.load(sys.stdin); c.update(ssid="LAB-TR069", password="senha-forte-123"); print(json.dumps(c))')"

# Forçar um Inform agora / ver histórico de sessões
post tr069/inform
get tr069/sessions | python3 -c 'import sys,json;[print(r["time"],r["actor"],r["action"],"|",r["result"][:120]) for r in json.load(sys.stdin)[:15]]'

# Diagnósticos
post diagnostics/ping '{"target":"1.1.1.1"}'
post diagnostics/traceroute '{"target":"1.1.1.1"}'
post diagnostics/speedtest

# PPPoE: conta no BRAS + discador
put bras  '{"service_name":"internet","accounts":[{"username":"lab","password":"lab123"}]}'
put pppoe '{"enabled":true,"username":"lab","password":"lab123","service_name":"internet","mru":0}'
get wifi/clients | python3 -c 'import sys,json;print(json.load(sys.stdin)["pppoe"])'

# Quatro hosts simulados reportados ao ACS
put lab/simulated-hosts '{"count":4}'
```

Principais rotas:

| Método | Rota | Uso |
|---|---|---|
| GET | `dashboard`, `events` (SSE) | Snapshot do sistema |
| GET/PUT | `wifi`, `lan`, `dhcp`, `firewall`, `tr069/config`, `pppoe`, `bras` | Configuração por seção |
| GET | `wan`, `interfaces`, `wifi/clients`, `wifi/radios`, `dhcp/leases` | Estado real do Linux |
| GET | `tr069/parameters`, `tr069/sessions`, `tr069/diagnostics/{basic,periodic,xmpp,stun}` | TR-069 |
| POST | `tr069/inform`, `tr069/{xmpp,stun}/reconnect` | Ações CWMP |
| POST | `diagnostics/{ping,traceroute,dns,speedtest}` | Diagnóstico de rede |
| GET/PUT | `lab/hosts`, `lab/simulated-hosts` | Hosts reportados ao ACS |
| GET/POST | `backups`, `backups/{id}/restore` | Snapshots cifrados |
| GET | `logs` | Auditoria |

---

## Operação: serviços, logs e atualização

```bash
# Estado
systemctl status lab-router-{helper,api,cwmp,ap,dhcp}
systemctl is-active lab-router-{bras,pppoe}

# Logs
journalctl -u lab-router-cwmp -f        # sessões com o ACS
journalctl -u lab-router-helper -f      # aplicação de rede / rollback
journalctl -u lab-router-ap -f          # hostapd
sudo tail -f /var/lib/lab-router-network/pppoe.log   # pppd (discador)

# Clientes no AP / regras ativas
iw dev ap-tr069 station dump
sudo nft list table inet lab_router
```

No painel, **TR-069 → Histórico de sessões** mostra cada Inform, cada RPC recebido, os nomes que o ACS leu (`GetParameterValues.nomes`), os parâmetros gravados (`SetParameterValues.parametros`, em texto puro) e o motivo de cada fault.

### Atualizar após mudar o código

```bash
make build
sudo install -m 0755 bin/lab-router /usr/local/lib/lab-router/lab-router
sudo install -m 0644 deploy/lab-router-*.service /etc/systemd/system/ && sudo systemctl daemon-reload
# Reinicie os três juntos: helper, api e cwmp compartilham o formato da configuração
sudo systemctl restart lab-router-helper lab-router-api lab-router-cwmp
```

Não é preciso rodar o `install.sh` de novo para atualizar. Reiniciar o helper reaplica a rede (o AP só reinicia se a configuração Wi-Fi mudou).

---

## Parâmetros TR-069 expostos

Raiz `InternetGatewayDevice.` (W = gravável pelo ACS):

| Área | Parâmetros |
|---|---|
| Identidade | `DeviceInfo.{Manufacturer,ManufacturerOUI,ProductClass,SerialNumber,SoftwareVersion,UpTime}` |
| Recursos | `DeviceInfo.ProcessStatus.CPUUsage`, `DeviceInfo.MemoryStatus.{Total,Free}` |
| Gerência | `ManagementServer.{URL,Username,Password,PeriodicInformEnable,PeriodicInformInterval,ConnectionRequestUsername,ConnectionRequestPassword}` (W), `STUN*` (W), `ConnReqAllowedJabberIDs` (W), `ConnReqJabberID`, `UDPConnectionRequestAddress`, `NATDetected` |
| Wi-Fi | `LANDevice.1.WLANConfiguration.1.{SSID,Enable,Channel,PreSharedKey.1.KeyPassphrase,X_LAB_SecurityMode,X_LAB_APIsolation}` (W), `BSSID`, `Status`, `TotalAssociations`, `Total{Bytes,Packets}{Sent,Received}` |
| Clientes Wi-Fi | `…WLANConfiguration.1.AssociatedDevice.{i}.{AssociatedDeviceMACAddress,AssociatedDeviceIPAddress,AssociatedDeviceAuthenticationState,LastDataTransmitRate,X_LAB_SignalStrength,X_LAB_Simulated}` |
| Hosts LAN | `LANDevice.1.Hosts.Host.{i}.{MACAddress,IPAddress,HostName,AddressSource,LeaseTimeRemaining,InterfaceType,Layer2Interface,Active,X_LAB_Simulated}`, `Hosts.HostNumberOfEntries` |
| DHCP | `LANDevice.1.LANHostConfigManagement.{DHCPServerEnable,MinAddress,MaxAddress}` (W), `IPRouters` |
| WAN | `WANDevice.1.WANCommonInterfaceConfig.Total{Bytes,Packets}{Sent,Received}`, `…WANIPConnection.1.{ConnectionStatus,Stats.*}` |
| PPPoE | `…WANPPPConnection.1.{Enable,Username,Password,PPPoEServiceName,MaxMRUSize}` (W), `ConnectionStatus`, `LastConnectionError`, `ExternalIPAddress`, `RemoteIPAddress`, `DNSServers`, `Uptime`, `PPPoEACName`, `Stats.*` |
| TR-143 | `DownloadDiagnostics.*`, `UploadDiagnostics.*` (URL, `DiagnosticsState`, `DSCP`, `TestFileLength` graváveis), `X_LAB_ThroughputMbps`, `Capabilities.PerformanceDiagnostic.*` |

Observações:

- **Senhas são somente escrita**: a leitura sempre volta vazia (padrão TR-098).
- Contadores de bytes são enviados como `unsignedLong` (64 bits) para gráficos de taxa não terem picos falsos a cada 4 GB.
- Em **todo Inform** vão também CPU, memória livre, clientes Wi-Fi e contadores WAN/WLAN — o ACS ganha uma amostra por sessão periódica.

---

## Integração com o ACS (widgets)

Sugestões para painéis do ACS (caminho do objeto **sem** índice e sem ponto final):

| Widget | Configuração |
|---|---|
| Tabela de clientes Wi-Fi | Objeto `InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.AssociatedDevice`; colunas `AssociatedDeviceMACAddress`, `AssociatedDeviceIPAddress`, `X_LAB_SignalStrength`, `LastDataTransmitRate`, `X_LAB_Simulated` |
| Tabela de hosts | Objeto `InternetGatewayDevice.LANDevice.1.Hosts.Host`; colunas `HostName`, `IPAddress`, `MACAddress`, `Active` |
| Medidor speedtest | `InternetGatewayDevice.DownloadDiagnostics.X_LAB_ThroughputMbps` (0–300 Mbps) |
| Medidor CPU | `InternetGatewayDevice.DeviceInfo.ProcessStatus.CPUUsage` (0–100 %) |
| Gráfico tráfego WAN | `…WANCommonInterfaceConfig.TotalBytesReceived`/`TotalBytesSent`, transformação **Taxa**, escala **8** (bits/s) |

> Cuidado: widgets que disparam uma tarefa de atualização a cada exibição podem encher a fila do ACS e travar o processo CWMP. Prefira os valores que já chegam no Inform.

---

## Desenvolvimento e testes

```bash
make test                 # go test -race + go vet
npm --prefix web run build
make dev-api              # API local em modo observação (dados em ./var)
make dev-web              # Vite com hot reload em http://127.0.0.1:5173 (proxy /api → 8088)

# Mock visual (sem Linux nem ACS; nenhuma operação executada)
cd web && VITE_LAB_VISUAL_MOCK=true npm run dev
```

Os testes Go cobrem: sessão SOAP completa contra ACS local, faults, escrita atômica com rollback, Digest Connection Request, XMPP com TLS (inclusive certificado autoassinado com opt-in), STUN/HMAC, TR-143 (sucesso e erros), hosts reais/simulados, PPPoE (opções geradas, classificação de erros do pppd, provisionamento pelo ACS), criptografia, backup e CSRF.

Estrutura:

```
cmd/lab-router/        entrada única (modos api, cwmp, helper, init, …)
internal/api/          HTTP, sessões, SSE; ui/ = frontend compilado (embed)
internal/cwmp/         agente TR-069, modelo TR-098, TR-143, hosts, PPP
internal/traversal/    XMPP e STUN
internal/network/      helper root, render de hostapd/dnsmasq/nftables, PPPoE
internal/service/      aplicação transacional da configuração
internal/store/        SQLite cifrado, auditoria, sessões, backups
internal/system/       leitura do Linux, diagnósticos, speedtest
web/src/               painel React (main.tsx, views.tsx, MechanismDiagnosis.tsx)
deploy/                units systemd
scripts/               inspeção e validações ao vivo
docs/                  guias detalhados
```

---

## Segurança e isolamento

- **WAN preservada**: nada altera `wlp2s0`, NetworkManager, rota padrão, DNS do host, Docker/VPN ou `/etc/nftables.conf`.
- **Privilégio mínimo**: API e CWMP rodam como `lab-router`; só o helper é root, sandboxed (`ProtectSystem=strict`) e acessível por socket Unix local.
- **Painel só em loopback**, sessão com cookie + CSRF, verificação de `Host`/`Origin`.
- **Laboratório isolado**: clientes do AP não alcançam o host, redes privadas, rotas corporativas nem a VPN; sem encaminhamento IPv6.
- **Segredos cifrados** no SQLite com chave mestra local (`/var/lib/lab-router`, modo 0700). Backups também cifrados.
- **Rollback**: qualquer falha ao aplicar rede restaura a configuração anterior.
- **FactoryReset pelo ACS desabilitado** por padrão.

Escolhas deliberadas de laboratório (não use credenciais reais de produção):

- O histórico de sessões registra os valores enviados pelo ACS **em texto puro**, inclusive senhas.
- XMPP tem opção para **aceitar certificado TLS sem verificação** (servidores autoassinados).
- Periodic Inform aceita intervalos a partir de **5 s**.

---

## Limitações conhecidas

- **Um rádio**: AP preso ao canal da WAN; 2.4 GHz independente exige outro adaptador.
- **Hosts simulados** existem só no relatório TR-069: não geram tráfego nem respondem a ping.
- **TR-143** com uma única conexão TCP; `Interface` é armazenado mas o tráfego segue a rota do sistema.
- **PPPoE** é um enlace de teste até o BRAS simulado; o tráfego dos clientes continua saindo pela WAN. Sem porta Ethernet, não disca em BRAS real.
- **TR-181** não implementado; IPPing/TraceRoute via ACS (TR-143) não implementados.
- STUN só funciona se o servidor STUN responder e a rota até ele sair pela WAN.

---

## Solução de problemas

| Sintoma | Causa provável / ação |
|---|---|
| Ping falha com "Operação não permitida" | Unit da API sem `CAP_NET_RAW`: reinstale `deploy/lab-router-api.service` |
| `fault 9002 … configuração recusada pelo helper` | Binários de versões diferentes: reinicie `helper`, `api` e `cwmp` juntos |
| Sessões com `EOF` / `connection refused` no ACS | Processo CWMP do ACS travado; teste `curl -X POST http://ACS:7547/` |
| XMPP: "certificado não confiável" | Servidor autoassinado: ligue "Aceitar certificado TLS sem verificação" |
| XMPP/STUN não salva | Faltam credenciais de Connection Request **ou** usuário/senha CWMP |
| PPPoE `ERROR_AUTHENTICATION_FAILURE` | Usuário/senha não batem com uma conta do BRAS |
| PPPoE `ERROR_NO_ANSWER` | Service-Name diferente do anunciado pelo BRAS, ou BRAS parado |
| Wi-Fi não sobe | `journalctl -u lab-router-ap`; o AP só opera no canal da WAN |

---

## Documentação complementar

- [Guia de instalação, uso e recuperação](docs/GUIA-DE-AUXILIO.md)
- [TR-069, Periodic Inform, XMPP e STUN](docs/TR069-XMPP-STUN.md)
- [Arquitetura, segurança e protocolo](docs/ARQUITETURA.md)
- [Resultados e limitações por módulo](docs/VALIDACAO.md)
