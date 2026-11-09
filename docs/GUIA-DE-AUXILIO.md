# Guia de auxílio — LAB Router

## 1. Estado entregue

Projeto em `~/labrouter`. Painel: **http://127.0.0.1:8088**.

**Atualização: instalação autorizada e concluída.** API/helper/AP/DHCP/CWMP estão ativos sob systemd, em live mode. A prévia transitória foi parada. O AP está em `LAB-TR069-REAL`, canal 40 / 5200 MHz, e a LAN em `192.168.50.1/24`. A inicialização automática após reboot continua desabilitada.

O banco, a identidade da CPE e a senha administrativa da prévia foram migrados para `/var/lib/lab-router`. A senha administrativa vigente foi preservada. Se ela ainda não foi alterada, a senha inicial pode ser consultada localmente:

```bash
sudo cat /var/lib/lab-router/initial-admin-password
```

Veja [o estado instalado e o guia atualizado de XMPP/STUN](TR069-XMPP-STUN.md). As seções de instalação abaixo continuam como referência para reinstalação e outras máquinas.

O login pede somente senha; não há campo de usuário. Troque a senha em **Sistema → Usuários e segurança**. Após a troca, o arquivo da senha inicial não é atualizado: ele deixa de representar a senha válida. A troca revoga todas as sessões.

## 2. Antes de ativar

Na inspeção inicial:

| Item | Valor observado |
|---|---|
| Ubuntu | 24.04.3 LTS |
| Kernel | 7.0.0-38-generic |
| WAN | `wlp2s0`, `192.0.2.64/24` |
| Gateway WAN | `192.0.2.1` |
| SSID WAN | `MinhaRede-5G` |
| Canal / frequência | 40 / 5200 MHz, banda de 5 GHz |
| AP | `ap-tr069`, MAC `02:35:32:77:f1:c6`, sem IPv4, desativada |
| Rádio físico | `phy0`, RTL8822CE / rtw88 |
| Rádios de teste | `wlan0`, `wlan1`, mac80211_hwsim; não emitem sinal físico |
| Forwarding IPv4 | Já estava habilitado (`1`) |
| Outros ambientes | Docker, LXC, VPN `tun0` e regras Kaspersky presentes |
| hostapd / dnsmasq | Nenhum processo encontrado no momento da inspeção |

Esses valores podem ter mudado. Execute novamente:

```bash
cd ~/labrouter
./scripts/inspect.sh
sudo nft list ruleset
sudo sysctl net.ipv4.ip_forward
```

Não execute `nft flush ruleset`, `iptables -F`, `nmcli networking off`, `systemctl restart NetworkManager`, `killall hostapd` nem altere `wlp2s0`. Esses comandos não fazem parte do procedimento do projeto.

Confirme que `192.168.50.0/24` não conflita com interfaces, rotas VPN ou redes necessárias. O helper verifica sobreposição com os endereços das outras interfaces; revise **também as rotas** manualmente, pois redes remotas podem existir sem endereço local.

Se aparecer outro hostapd, identifique primeiro seu PID, interface e configuração. O instalador não mata esse processo nem migra automaticamente `/tmp/hostapd-real-tr069.conf`.

## 3. Compilar e instalar

Compile como usuário normal:

```bash
cd ~/labrouter
make build
make test
```

Se faltar compilador C: instale `build-essential` antes de compilar. O binário usa o driver SQLite com CGO e inclui os arquivos estáticos do frontend.

Pare a prévia para liberar a porta 8088:

```bash
systemctl --user stop lab-router-dev.service
```

Execute o instalador:

```bash
sudo ./install.sh
```

Ele inspeciona o ambiente, apresenta a proposta e solicita a frase **ATIVAR LAB ROUTER**. Sem essa confirmação, não instala nem ativa serviços. Depois pergunta separadamente se deve iniciar após reboot.

A instalação cria:

| Caminho | Finalidade |
|---|---|
| `/usr/local/lib/lab-router/lab-router` | Um executável, com modos API, helper e CWMP |
| `/etc/lab-router/environment` | Bind, diretório de dados e live mode |
| `/var/lib/lab-router/` | SQLite, chave mestra, backups cifrados e senha inicial |
| `/var/lib/lab-router-network/` | Configurações root de AP/DHCP/firewall e último estado aplicado |
| `/var/lib/lab-router-dhcp/leases` | Concessões DHCP |
| `/run/lab-router-helper/helper.sock` | Canal Unix restrito API/helper |
| `/etc/systemd/system/lab-router-*.service` | Cinco unidades próprias |

Por padrão, uma instalação nova tem banco separado da prévia. Nesta máquina, foi usada a opção `LAB_IMPORT_DATA=$PWD/var` para migrar o banco com backup consistente SQLite, preservar chave/senha/serial e ajustar o proprietário. Essa opção só importa quando ainda não existe banco no destino. Sem importação, senha e identidade podem ser diferentes. Para conservar essa identidade, não copie banco/chave enquanto houver processos usando-os: pare os serviços, copie **todo o diretório de dados** com proprietário/permissões corretos e mantenha uma cópia recuperável. A configuração anterior do hostapd em `/tmp` não é importada automaticamente.

A senha do AP é aleatória em uma instalação nova. Consulte localmente:

```bash
sudo cat /var/lib/lab-router/initial-admin-password
sudo /usr/local/lib/lab-router/lab-router show-wifi-password
```

A segunda operação é permitida somente ao root local. Para trocar a senha do AP, use **Rede Wi-Fi → Segurança** e salve. Deixar o campo de senha vazio mantém a senha anterior; não significa configurar rede aberta.

### Se o instalador recusar

- **Subnet conflitante:** não remova rotas nem altere a LAN corporativa. Escolha outro segmento. Antes da ativação, o painel de observação recusa alterações de rede; a instalação padrão só avança com a rede padrão sem conflito. Para provisionamento inicial personalizado, ajuste os defaults em `internal/model/config.go`, compile e revise a proposta do instalador antes de autorizar.
- **Forwarding em 0:** o instalador não altera esse sysctl global automaticamente. Revise o uso por Docker/VPN/LXC antes de habilitar `net.ipv4.ip_forward=1`; não desabilite posteriormente se outro serviço precisar dele.
- **Policy forward drop:** a cadeia própria não consegue superar um drop de outra cadeia base. O instalador recusa esse cenário. A integração com o firewall preexistente exige revisão e autorização específicas; não há modificação automática das regras Docker/UFW/Kaspersky.
- **hostapd externo:** encerre somente a instância identificada que usa `ap-tr069`, quando você decidir migrá-la. Preserve o arquivo anterior. Depois repita a inspeção.
- **Porta 8088 ocupada:** verifique `ss -lntp` e se a prévia continua ativa. Não encerre processos de outros projetos.
- **Aplicação falhou após instalar os arquivos:** consulte os logs abaixo. O helper tenta reverter a transação de rede. A instalação de arquivos/unidades em si não é uma transação de pacotes; não presuma que nada foi instalado. Corrija a causa e repita ou desinstale.

## 4. Conferir os serviços

```bash
systemctl status lab-router-api lab-router-helper lab-router-ap lab-router-dhcp lab-router-cwmp
journalctl -u lab-router-api -u lab-router-helper -u lab-router-ap -u lab-router-dhcp -u lab-router-cwmp -n 120 --no-pager
ip -4 addr show dev ap-tr069
iw dev ap-tr069 info
iw dev wlp2s0 info
sudo hostapd_cli -p /run/lab-router-hostapd -i ap-tr069 status
sudo nft list table inet lab_router
sudo nft list table ip lab_router_nat
```

A API e o agente CWMP executam como `lab-router`, sem privilégios. O helper executa como root e possui uma API restrita por socket Unix; ele não recebe comandos shell nem nomes de interface arbitrários.

O dashboard mostra dados reais. Em observação, o helper/clientes/firewall podem aparecer indisponíveis. O tráfego da WAN inclui o tráfego do notebook, e não somente o dos clientes do laboratório. A lista de leases omite concessões expiradas; não existe botão funcional de limpeza forçada do arquivo ativo.

### Canal e banda

O AP deve permanecer no canal atual da WAN. Canal 40 = **5200 MHz**, ou 5.2 GHz dentro da banda de 5 GHz. Não é 5.8 GHz.

A página 2.4 GHz explica a indisponibilidade nesse rádio compartilhado. Um segundo adaptador físico é detectado na lista de rádios, mas o provisionamento de outro AP/BSSID ainda exige implementação/manual revisão. Não há dual-band simultâneo neste projeto entregue.

Somente largura de 20 MHz está validada nesta versão. A potência permanece sob controle do driver. WPA2 é a opção inicial; WPA3/misto têm configuração SAE/MFP, mas dependem do driver e precisam de teste físico de associação. Se o AP não confirmar o estado e o SSID, o helper tenta rollback.

O helper acompanha o canal da WAN e as rotas. Se o canal mudar ou a WAN desconectar, para os serviços do AP para preservar a WAN e registra o motivo em `/api/wifi/clients` / status do helper. Reaplique as configurações Wi-Fi no canal atual depois de recuperar a WAN. Esse comportamento ainda precisa de validação física.

## 5. Testar celular, DHCP e NAT

1. Procure `LAB-TR069-REAL` no celular e autentique com a senha consultada/configurada.
2. Confirme um IP `192.168.50.100` a `.200`, máscara `/24`, gateway `192.168.50.1` e DNS públicos configurados.
3. Confirme o lease no painel **Rede Local → Servidor DHCP** e a estação em **Rede Wi-Fi → Clientes conectados**.
4. Abra um site público no celular e observe os contadores das tabelas próprias.
5. Tente acessar o gateway da sala, por exemplo `192.0.2.1`, e um destino corporativo autorizado para teste: devem estar bloqueados para o cliente.
6. Confira que o notebook continua conectado ao SSID da sala e à VPN, com as mesmas rotas necessárias.

A implementação bloqueia acesso dos clientes ao host, exceto DHCP. O painel não é disponibilizado pela LAN nesta versão; isso é intencional e aparece como uma limitação. Os DNS são distribuídos diretamente aos clientes. **Não há resolvedor DNS local**; dnsmasq usa `port=0`.

O IPv6 dos clientes é bloqueado nas cadeias input/forward do laboratório. Não há roteamento IPv6 nem DHCPv6 implementado.

## 6. Configurar TR-069 / ACS Beta

Abra **TR-069 / CWMP → Configuração do ACS**.

| Campo | Valor / orientação |
|---|---|
| URL ACS | `http://acs.example.com:7547/` |
| Usuário / senha CWMP | Credenciais de dispositivo fornecidas pelo ACS, se exigidas |
| Habilitar TR-069 | Ative quando estiver pronto para sessões reais |
| Periodic Inform | Habilitado se desejado; sugestão: 300 segundos |
| Identidade da CPE | Fabricante, OUI de laboratório, ProductClass e serial estável |
| Painel do ACS | `http://acs.example.com/` — é outra interface, não o endpoint CWMP |

**Não use automaticamente o login do painel ACS como autenticação CWMP.** Connection Request, STUN e XMPP possuem credenciais separadas. A API cifra a configuração com AES-GCM; a chave fica em arquivo separado no diretório de dados, modo 0600. Senhas são omitidas de respostas e logs da aplicação. Os arquivos hostapd precisam conter o segredo em texto para o daemon e ficam em diretório root, modo 0700, arquivos 0600.

HTTP não fornece confidencialidade de transporte. Use o endpoint HTTP apenas no laboratório autorizado; prefira HTTPS quando disponibilizado pelo ACS. O cliente valida certificados HTTPS e não segue redirects do endpoint automaticamente.

Abra **Diagnóstico CWMP** e pressione **Enviar Inform agora**. A resposta imediata “enfileirado” significa apenas que o pedido foi salvo. Aguarde o histórico de sessão: sucesso exige InformResponse SOAP e encerramento HTTP apropriado. O serviço `lab-router-cwmp` precisa estar ativo para consumir a fila.

### O que já foi comprovado no Beta

Uma sessão real com `http://acs.example.com:7547/` foi concluída em **2026-11-09 17:01:06 UTC**:

- Inform enviado e InformResponse interpretado.
- ACS enviou GetParameterNames; a CPE respondeu.
- Sessão encerrada por HTTP 204, sem erro SOAP.

Evidência local: `artifacts/beta-session.json`. O CWMP da prévia foi desativado novamente após o teste. Não foram utilizadas credenciais do painel web ACS.

Isso não comprova que você já visualizou a CPE no painel do ACS nem que houve alteração física de SSID. Localize a CPE pelo serial exibido no painel LAB Router; a instalação definitiva poderá ter outro serial, conforme explicado acima.

### Teste principal: SSID por CWMP verdadeiro

Faça este teste depois que o AP e o DHCP estiverem operacionais:

1. Anote o SSID atual e mantenha acesso ao painel pelo notebook/localhost.
2. No ACS, localize a CPE pelo OUI/ProductClass/Serial.
3. Solicite GetParameterValues de:
   `InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID`
4. Solicite SetParameterValues desse mesmo parâmetro, tipo `xsd:string`, para um SSID temporário, por exemplo `LAB-TR069-ACS-TESTE`.
5. Se Connection Request remoto não estiver disponível, pressione **Enviar Inform agora** no LAB Router para que a CPE busque a tarefa na sessão iniciada por ela. Não basta atualizar a página do ACS.
6. Confira no histórico que SetParameterValues foi recebido e executado. Status 0 só é produzido após o serviço de configuração retornar sucesso; em live mode ele exige confirmação de estado/SSID/canal pelo hostapd/iw.
7. Verifique fisicamente:

   ```bash
   iw dev ap-tr069 info
   sudo hostapd_cli -p /run/lab-router-hostapd -i ap-tr069 status
   journalctl -u lab-router-helper -u lab-router-ap --since '5 minutes ago'
   ```

8. Procure o novo nome no celular; reconecte se necessário.
9. Execute nova leitura no ACS e confira o valor atualizado.
10. Restaure o SSID original pelo mesmo mecanismo.

O painel do ACS determina como criar tarefas GPV/SPV; nomes de botões variam entre GenieACS/SGPACS. Não há integração REST fingindo ser TR-069. Se usar API administrativa do GenieACS, siga a documentação da versão do seu servidor e sua política de autenticação; essa API somente agenda a tarefa, que precisa ser entregue pelo canal CWMP.

Parâmetros obrigatórios suportados:

```text
InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID
InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable
InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel
InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.PreSharedKey.1.KeyPassphrase
InternetGatewayDevice.ManagementServer.URL
InternetGatewayDevice.ManagementServer.PeriodicInformEnable
InternetGatewayDevice.ManagementServer.PeriodicInformInterval
```

A senha de segurança retorna string vazia em leitura. O canal do AP continua sujeito à proteção da WAN, mesmo quando a solicitação vem do ACS. Consulte tipos/permissões em **Parâmetros TR-098 / TR-181**; TR-181 ainda não está implementado e seus caminhos recebem fault de parâmetro desconhecido.

### Connection Request, STUN e XMPP

O listener HTTP Digest está limitado a **127.0.0.1:7548/connection-request**. Ele autentica e enfileira o evento `6 CONNECTION REQUEST`, mas não é acessível ao ACS externo nesse bind. Não anuncie uma URL pública que você não validou.

Para teste local, configure usuário/senha próprios e a URL `http://127.0.0.1:7548/connection-request`; use um cliente com Digest. Não publique o painel nem abra portas da sala para contornar isso.

STUN e XMPP agora estão implementados, com páginas específicas de configuração e diagnóstico. Consulte [o guia atualizado](TR069-XMPP-STUN.md): no Beta, ainda faltam conta XMPP/confiança TLS e resposta STUN válida. O listener HTTP continua loopback; os novos mecanismos não expõem o painel.

## 7. Erros comuns

| Sintoma | Verificação / ação |
|---|---|
| “Modo observação” ao salvar Wi-Fi | A prévia recusa escrita real. Instale/autorize live mode. |
| HTTP 401 / falha de autenticação ACS | Confira credenciais CWMP da CPE; não as confunda com login do painel ACS. |
| HTTP 200 sem InformResponse | Endpoint pode ser o painel web, um proxy ou resposta inválida; não é sucesso SOAP. |
| Inform fica enfileirado | Confira `lab-router-cwmp.service`, enabled e histórico. |
| SSID salvo falha com rollback | Consulte hostapd/helper; canal, driver, SAE/MFP e conflito de processo são causas possíveis. |
| AP não aparece em 2.4 GHz | Rádio está compartilhado com a WAN de 5 GHz. Não mude o canal da WAN. |
| Celular associa, mas não recebe IP | Confira serviço DHCP, pool, endereço do AP, leases e logs dnsmasq. |
| DHCP funciona, mas sem internet | Confira forwarding, NAT, contador de regras e outros firewalls; uma regra accept própria não supera drops externos. |
| Cliente acessa rede corporativa | Interrompa o AP do laboratório e revise o isolamento antes de continuar. |
| Reboot remoto não reinicia Ubuntu | Comportamento esperado: somente serviços virtuais AP/DHCP. |
| FactoryReset retorna 9001 | Operação desabilitada por padrão; política local exige ativação explícita. |

## 8. Backup e recuperação

Em **Sistema → Backup / Restore**, crie snapshots cifrados antes das alterações. Restore passa pelo mesmo serviço de configuração e validação de rede.

Para cópia externa consistente, pare API e CWMP antes de copiar **todo** `/var/lib/lab-router`, incluindo `master.key`. Uma cópia isolada do arquivo `.db` enquanto WAL está ativo pode ser inconsistente. Proteja o backup: a chave junto ao banco permite descriptografar os segredos. Os snapshots do painel não incluem configuração do Ubuntu, Docker, VPN ou contas externas do ACS.

Se o laboratório causar problemas:

```bash
sudo systemctl stop lab-router-cwmp lab-router-api lab-router-helper lab-router-ap lab-router-dhcp
```

Isso para somente os serviços do projeto. Parar o helper não remove automaticamente as tabelas próprias nem o IP do AP. Para retirar somente esses componentes, use o desinstalador:

```bash
cd ~/labrouter
sudo ./uninstall.sh
```

Ele exige **REMOVER LAB ROUTER**, remove serviços, endereço LAN registrado e tabelas identificadas como próprias. Preserva os dados cifrados e o usuário de serviço para recuperação. Não remove a interface `ap-tr069`, não altera NetworkManager e não desabilita o forwarding global.

## 9. Persistência após reboot

Somente depois dos testes físicos, habilite se desejar:

```bash
sudo systemctl enable lab-router-api lab-router-helper lab-router-cwmp
```

Não habilite AP/DHCP isoladamente. O helper restaura a configuração registrada e o isolamento antes de iniciar esses serviços, recusando conflito ou canal incompatível. A aplicação não cria `ap-tr069` automaticamente após reboot: a interface precisa existir e ser não gerenciada pelo NetworkManager. Essa preparação persistente ainda é manual e precisa ser validada no seu ambiente, sem alterar `wlp2s0`.

Se `ap-tr069` não existir, a criação original conhecida é:

```bash
sudo iw phy phy0 interface add ap-tr069 type __ap addr 02:35:32:77:f1:c6
```

Execute somente após conferir `iw dev`, o PHY correto e ausência da interface. Não execute o comando repetidamente nem use um rádio simulado por engano. Nesta entrega não foi instalado um serviço que cria a interface após reboot.
