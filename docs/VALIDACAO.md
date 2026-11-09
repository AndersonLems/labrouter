# Resultados da implementação e validação

> Registro da versão inicial. A instalação física e os mecanismos XMPP/STUN foram implementados depois; consulte [a validação atualizada](TR069-XMPP-STUN.md) para o estado vigente.

Data: 9 de novembro de 2026. A ativação física foi deixada para o usuário a seu pedido. Este documento distingue implementação, teste automatizado e evidência operacional.

## Resultado principal

**Uma sessão SOAP/CWMP real com o ACS Beta foi concluída.** O endpoint `http://acs.example.com:7547/` respondeu a Inform com InformResponse, enviou GetParameterNames e encerrou a sessão com HTTP 204. Último sucesso registrado: `2026-11-09T17:01:06.996934916Z`. Consulte `artifacts/beta-session.json`.

Não houve teste de alteração física de SSID pelo Beta. Não foram fornecidas credenciais do painel administrativo ACS e não foi verificada a listagem visual da CPE. Não há evidência de associação de celular, DHCP físico ou navegação de cliente pelo NAT nesta entrega.

## Módulos

| Módulo | Implementado | Testado / evidência | Ajustes / limites |
|---|---|---|---|
| Build | Go/Chi; SQLite; React/TS/Vite incorporado ao binário | `go build`, frontend build, `go vet` | SQLite usa CGO; build exige compilador C |
| Painel | Dark/light, sidebar, cards, SSE, formulários, confirmação, toast, responsividade, busca | Chrome headless: login, SSE com WAN real, desktop/móvel sem overflow | Algumas saídas técnicas usam JSON/texto em vez de tabelas estruturadas |
| Dashboard | IPs, MACs, rotas, WAN/AP, memória, CPU por deltas, uptime, tráfego e serviços | API lendo o Ubuntu; screenshots em artifacts | Tráfego inclui o notebook; CPU/tráfego precisam de duas amostras; consulta de clientes exige helper |
| Wi-Fi | Geração hostapd, SSID/senha/enable, canal protegido, limite, ocultação, isolamento, deny MAC, SAE/misto | Teste de escopo; parser hostapd em namespace isolado leu configuração e chegou à inicialização de driver | Sem ativação física; largura restrita a 20 MHz, sem ajuste de potência; SAE/misto dependem de validação no hardware |
| Rádios | Descoberta por sysfs/iw, identificação hwsim, restrição STA+AP | RTL8822CE, PHY e canal reais lidos | Segundo rádio é detectado, mas não provisionado automaticamente; multi-BSSID e dual-band simultâneo não implementados |
| LAN | IPv4/prefixo, aplicação de endereço somente na AP, verificação de sobreposição de endereços, atualização conjunta com DHCP | Validação de configuração e recusa de pool incoerente | Sem teste físico; revisar também rotas remotas; IPv6 bloqueado para clientes |
| DHCP | Geração dnsmasq, pool, duração, DNS, reservas, leitura de leases não expiradas | `dnsmasq --test` aceitou configuração com reserva | Sem cliente DHCP físico; sem limpeza forçada do arquivo ativo; sem resolvedor DNS local |
| NAT/firewall | Duas tabelas próprias, masquerade, DNAT, bloqueio de destinos privados/rotas conhecidas, tráfego somente via WAN, contadores | nftables `-c` e criação das duas tabelas em namespace de rede isolado | Sem tráfego físico; política drop externa é recusada; integração UFW/Docker/Kaspersky precisa de revisão quando houver drops |
| Helper | Socket Unix restrito, validação, serialização, comandos fixos via exec, escrita atômica, backups de configuração, tentativa de rollback, acompanhamento de WAN/rotas | Compilação, revisão, testes de renderização e falha do adaptador no serviço | Helper não instalado; rollback completo em rádio/serviços reais e recuperação após reboot ainda não testados |
| CWMP | SOAP 1.1 / CWMP 1.0, Inform/InformResponse, cookies HTTP, Basic/Digest, fila de eventos, retry, periodicidade, status e histórico | Sessão real Beta + testes ACS HTTP local | Compatibilidade limitada aos RPCs/modelo listados; sem certificação BBF |
| Leitura/escrita | GPV/GPN/SPV e GetRPCMethods; faults 9000/9001/9002/9003/9005/9006/9007/9008 conforme operação | ACS local enviou leitura, SPV de SSID, confirmação por nova leitura; falhas e atomicidade testadas | Adaptador Linux substituído por callback no teste local; não prova mudança física |
| Dados TR-098 | DeviceInfo, ManagementServer, subconjunto WLAN/LAN/WAN; senha de Wi-Fi write-only; ParameterKey | Tipos/permissões, nomes inválidos, read-only, tipos e valores inválidos | Modelo parcial; sem objetos dinâmicos AddObject/DeleteObject; TR-181 não implementado |
| Atributos CWMP | RPCs não anunciados | Método desconhecido gera fault | Get/SetParameterAttributes e notificações por atributo não implementados |
| Periodic Inform | Loop com intervalo configurável e retry exponencial | Código integrado e sessão direta testada | Não houve ensaio temporal prolongado do daemon em produção |
| Eventos | Fila persistente: BOOTSTRAP, BOOT, PERIODIC, VALUE CHANGE, CONNECTION REQUEST, M Reboot | Eventos informados, preservação após falha de Inform | RetryCount é 0 no Inform atual; retry usa backoff local, sem todas as regras de temporização TR-069 |
| Connection Request | HTTP Digest MD5, qop auth, nonce expirável e contagem antirreplay | Requisição sem credenciais recusada; Digest correto enfileira evento 6 | Listener exclusivamente loopback; sem alcance remoto nem STUN/XMPP |
| Reboot | Reinício lógico de AP/DHCP; nunca reboot do Ubuntu | RPC gerado/revisado; comando restrito ao helper | Execução real e verificação pós-reboot não testadas; falha do reinício após resposta é auditada |
| FactoryReset | Desabilitado por padrão; exige política local; repõe somente rede de laboratório, preservando identidade/ACS | Caminho de autorização implementado | Aplicação física não testada; não apaga sistema host ou contas externas |
| Diagnósticos | Ping, traceroute, DNS; diagnóstico DNS/TCP do ACS; Inform separado | Dados reais e argumentos validados | Status HTTP CWMP vem das sessões; diagnóstico independente não executa GET fingindo ser CWMP |
| Autenticação | bcrypt, cookies HttpOnly/SameSite, CSRF, restrição Host/Origin, rate limit de login, troca de senha/revogação | Testes API para acesso anônimo, CSRF, Origin, Host e vazamento de senha | Uma conta administrativa; HTTPS do painel e acesso LAN não provisionados |
| Segredos | AES-GCM na configuração/backup SQLite; chave separada 0600; respostas redigidas | Banco reaberto e restaurado; segredo não encontrado em texto no DB/WAL | Proteção depende das permissões/chave; hostapd exige segredo em arquivo root |
| Auditoria/backup | Auditoria SQLite, histórico CWMP, snapshots cifrados e restore pelo serviço comum | Testes de persistência, backup e recusa de commit após falha de aplicação | Sem retenção/rotação automática; logs de daemons consultados via journalctl |
| systemd | Cinco unidades: API, helper, AP, DHCP, CWMP | `systemd-analyze verify` em raiz isolada com executáveis/stubs de targets | Unidades de produção não instaladas; criação persistente da interface AP ainda manual |
| Instalador | Inspeção, proposta, confirmação, usuário/diretórios/unidades e ativação explícita; escolha separada de autostart | Sintaxe bash e revisão | Instalador live não executado; sem rollback da instalação de pacotes; projeto não está aprovado como roteador pronto para produção |
| Desinstalador | Remove só componentes identificados, preserva dados, interface e forwarding global | Sintaxe bash e revisão | Ainda não executado contra uma instalação real |
| Modos | Observação com dados reais; live condicionado ao helper; mock visual separado e sinalizado | Observação e recusa de escrita verificadas no navegador | Mock é para desenvolvimento visual e não prova operações Linux |

## Comandos de validação executados

```bash
go test -race -count=1 ./...
go vet ./...
npm --prefix web run build
npm --prefix web audit --omit=dev --audit-level=high
bash -n install.sh uninstall.sh scripts/inspect.sh
node scripts/browser.mjs
go run ./cmd/validate-configs
sudo unshare --net nft -c -f "$PWD/artifacts/generated/firewall.nft"
sudo unshare --net sh -c 'nft -f "$1" && nft list tables' sh "$PWD/artifacts/generated/firewall.nft"
dnsmasq --test --conf-file="$PWD/artifacts/generated/dnsmasq.conf"
sudo unshare --net hostapd "$PWD/artifacts/generated/hostapd.conf"
python3 scripts/probe-beta.py
```

O comando hostapd no namespace isolado termina com erro **esperado de ausência de interface/driver**: não há rádio dentro desse namespace. Essa observação valida a leitura inicial do arquivo, mas não equivale a AP-ENABLED. Não existe opção oficial `hostapd --test` usada aqui.

A validação systemd foi feita com `--root` apontando para `artifacts/systemd-root`: o binário de produção ainda não existe em `/usr/local/lib/lab-router`. Uma verificação direta no host produz esse aviso esperado, além de avisos de unidades externas preexistentes.

Os testes unitários/integrados passaram com detector de corrida; a auditoria npm das dependências de produção não encontrou vulnerabilidades no momento executado. Isso não substitui revisão de segurança.

## Preservação do ambiente

`wlp2s0` permaneceu associada ao SSID informado, com IP `192.0.2.64`, canal 40 / 5200 MHz ao final das verificações de observação. Nenhum comando deste projeto alterou WAN, NetworkManager, Docker, LXC ou VPN. Os testes nftables/hostapd de kernel foram executados em namespaces de rede temporários.

As capturas completas de interfaces/rotas não são idênticas: durante o trabalho desapareceram uma veth de Docker, uma bridge passou a linkdown e o endereço IPv6 link-local da VPN mudou. Não foram emitidas operações para realizar essas mudanças; o ambiente possui outros processos ativos. Por isso não se declara equivalência global de todos os snapshots. Os arquivos `artifacts/interfaces-*.json` e `artifacts/routes-*.json` permitem comparação local.

## Artefatos

- `artifacts/dashboard-desktop.png`, `dashboard-mobile.png`: prévia no navegador.
- `artifacts/wifi-capabilities.png`: explicação da limitação de banda.
- `artifacts/beta-session.json`: resultado da sessão SOAP Beta, sem credenciais.
- `artifacts/generated/`: configurações de teste; senha Wi-Fi aleatória apenas para parser, não utilizada no AP.
- `internal/*/*_test.go`: testes automatizados reproduzíveis.

Os dados privados de execução e capturas do sistema estão excluídos pelo `.gitignore`.
