# Estado instalado e diagnóstico TR-069, Periodic Inform, XMPP e STUN

Atualização: 9 de novembro de 2026.

## Instalação nesta máquina

- Painel: **http://127.0.0.1:8088**, live mode, autenticação obrigatória.
- AP: **LAB-TR069-REAL**, WPA2, `ap-tr069`, MAC `02:35:32:77:f1:c6`.
- Canal: **40 / 5200 MHz**, compartilhado com `wlp2s0`.
- LAN: **192.168.50.1/24**; DHCP **192.168.50.100–200**, concessão de 12 horas.
- API, helper, hostapd, dnsmasq e CWMP ativos sob unidades próprias systemd.
- Identidade da CPE e credencial administrativa da prévia preservadas na migração.
- Autostart após reboot **desabilitado**. Fechar o terminal não para os serviços.

Consulte a senha Wi-Fi localmente, sem gravá-la em logs:

```bash
sudo /usr/local/lib/lab-router/lab-router show-wifi-password
```

A senha administrativa vigente continua a mesma. O arquivo inicial, se a senha ainda não foi trocada, é `/var/lib/lab-router/initial-admin-password`. Os dados em `lab-router/var` são agora uma cópia histórica da prévia; o banco em uso fica em `/var/lib/lab-router`.

A WAN permaneceu conectada ao SSID `MinhaRede-5G`, IP `192.0.2.64`, canal 40 durante os testes físicos. A comparação de nftables antes/depois da instalação não encontrou remoção de objetos externos ao LAB Router; surgiram três objetos adicionais de Docker durante o intervalo. O projeto manipula apenas `inet lab_router` e `ip lab_router_nat`.

O teste físico de SSID pelo painel mudou o AP para `LAB-TR069-VALIDACAO` e restaurou `LAB-TR069-REAL`. A confirmação veio de `iw`, não apenas do banco. DHCP iniciou com sockets vinculados exclusivamente à AP. A associação de celular, concessão efetiva a um cliente físico, navegação por NAT e isolamento ponta a ponta ainda precisam ser conferidos com um cliente.

## 1. TR-069 básico

Cliente nativo **SOAP 1.1 / CWMP 1.0**, usando sessões HTTP reais:

- Inform / InformResponse e sequência de POSTs da sessão.
- GetRPCMethods, GetParameterNames, GetParameterValues e SetParameterValues.
- Parâmetros TR-098 explícitos, faults e serviço comum de configuração.
- Reboot lógico e FactoryReset restrito/desabilitado por padrão.
- Basic/Digest para ACS, cookies e validação TLS quando o ACS usa HTTPS.

O ACS Beta em `http://acs.example.com:7547/` já confirmou sessões reais. A CPE respondeu a GetParameterNames enviado pelo Beta. Não foi usada uma API REST para substituir o protocolo.

**Diagnóstico:** menu **TR-069 / CWMP → Diagnóstico TR-069**. Mostra último Inform, código HTTP, erro, sessão, eventos e histórico SOAP. “Enviar Inform agora” somente enfileira; o histórico posterior comprova ou rejeita o sucesso.

O serial que identifica a CPE está em **Identidade da CPE**. A listagem visual da CPE e a criação de tarefas no painel do Beta continuam dependendo do acesso administrativo ao ACS.

## 2. Periodic Inform — 60 segundos

Nesta instalação foi persistida a configuração:

```text
enabled = true
periodic_enable = true
interval = 60
```

O daemon está estabelecendo sessões com o Beta. Foram observados Informs com evento **2 PERIODIC** em:

```text
2026-11-09 17:26:57 UTC
2026-11-09 17:27:58 UTC
```

Ambos tiveram sessão SOAP concluída. A diferença observada foi de 61 segundos devido à resolução do loop e ao processamento; o intervalo configurado é de 60 segundos. Falhas de transporte/autenticação acionam backoff, portanto não se deve esperar um novo Inform exatamente a cada 60 segundos durante uma falha.

**Diagnóstico:** **Diagnóstico Periodic Inform**. Compare horários, eventos `2 PERIODIC`, estado e próximo Inform. Pedidos manuais e Connection Requests são eventos diferentes.

Evidência: `artifacts/periodic-beta.json`. Reprodução opcional:

```bash
cd ~/labrouter
python3 scripts/check-periodic.py
```

O script habilita o CWMP e o intervalo de testes no ACS configurado, aguardando duas sessões periódicas. Usa a senha administrativa inicial; se ela já foi trocada, configure/teste pela interface em vez de editar segredos no script.

## 3. XMPP Connection Requests

O cliente abre uma **conexão de saída**, exige STARTTLS com certificado confiável, autentica via SASL, faz resource bind e envia presença. A biblioteca Mellium é reutilizada para XMPP/TLS/SASL; o adaptador interpreta o IQ de Connection Request do Broadband Forum.

Em **TR-069 / CWMP → XMPP**, configure:

| Campo | Significado |
|---|---|
| Servidor e porta TCP | Host alcançável do servidor XMPP; candidato Beta: `acs.example.com:5222` |
| Domínio XMPP | Domínio do JID e identidade TLS; pode ser diferente do host TCP |
| Conta XMPP | Usuário local ou JID `usuario@dominio` |
| Senha da conta XMPP | Autenticação SASL; independente da senha CWMP |
| Recurso | Solicitação de recurso; o JID efetivamente vinculado aparece no diagnóstico |
| JIDs autorizados | Remetentes ACS permitidos; um bare JID permite seus recursos |
| Usuário/senha Connection Request | Credenciais verificadas dentro do IQ TR-069; independentes da conta XMPP |

O IQ deve ser `type="get"`, com ID, remetente permitido e payload no namespace:

```text
urn:broadband-forum-org:cwmp:xmppConnReq-1-0
```

A CPE compara username/password de Connection Request sem registrar os valores. Responde IQ result apenas quando autentica e enfileira; retorna not-authorized/service-unavailable nos casos correspondentes. Pedidos repetidos com o mesmo remetente/ID são rejeitados na janela de proteção da conexão.

**Diagnóstico:** **Diagnóstico XMPP**. Separa TCP, TLS, SASL, JID vinculado, eventos aceitos/rejeitados e correlação com sessão CWMP. “Reconectar mecanismo” reinicia a conexão real, sem abrir portas de entrada na WAN.

### Situação do Beta

Uma negociação STARTTLS de diagnóstico encontrou um **certificado autoassinado**. O cliente exige confiança TLS e não usa `InsecureSkipVerify`. Não foi tentado login SASL com credenciais inventadas.

Para prosseguir:

1. Obtenha do administrador do ACS o domínio/JID correto, a conta XMPP da CPE, senha e a CA/certificado autorizado.
2. Confirme a identidade e a impressão digital do certificado por um canal confiável.
3. Se a política do laboratório autorizar, instale a CA correta no trust store do Ubuntu e reinicie somente `lab-router-cwmp`.
4. Configure os campos no painel e habilite XMPP.
5. Envie um Connection Request real pelo ACS e confira `cwmp_success` na linha do pedido.

Não importe automaticamente um certificado obtido pela mesma conexão sem verificar sua identidade. O projeto não mudou o trust store do Ubuntu.

O servidor deve conseguir rotear o IQ até o **JID completo efetivamente vinculado**, exibido no diagnóstico e publicado em `ManagementServer.ConnReqJabberID`. A lista permitida usa `ManagementServer.ConnReqAllowedJabberIDs`.

## 4. STUN / UDP Connection Requests

O cliente suporta dois modos explícitos:

- **RFC 3489 / TR-111 legado:** transação de 128 bits, MAPPED-ADDRESS, atributos CONNECTION-REQUEST-BINDING e BINDING-CHANGE, USERNAME e MESSAGE-INTEGRITY após challenge 401.
- **RFC 5389:** magic cookie, MAPPED/XOR-MAPPED-ADDRESS, challenge 401/438 com realm/nonce e autenticação long-term quando exigida.

O keepalive usa período **fixo**, padrão de 30 segundos, configurável entre 15 e 600 segundos. Não há descoberta automática do tempo de expiração do NAT, classificação do tipo de NAT ou suporte completo RFC 5780. Isso não é apresentado como implementação integral/certificada do Annex G.

Em **STUN / NAT Traversal**, configure servidor, porta, modo, keepalive e, quando aplicáveis, usuário/senha **STUN**. A porta local `0` escolhe uma porta UDP efêmera; uma porta fixa deve ser ≥1024. O socket é associado ao IPv4 de `wlp2s0`; não são criadas regras globais de entrada.

O mesmo socket recebe Binding Responses e UDP Connection Requests. Estes últimos precisam de formato HTTP GET absoluto e query TR-111 com `ts`, `id`, `un`, `cn` e `sig`. A assinatura HMAC-SHA1 usa a senha **Connection Request**, não a senha STUN. O cliente verifica origem IP do servidor, assinatura, timestamp crescente e ID, persistindo o estado antirreplay. Não envia resposta HTTP/UDP ao pedido.

O modo RFC 3489 aplica o formato de integridade legado; o modo RFC 5389 foi verificado também contra o vetor público do RFC 5769. Binding sem autenticação exigida pelo servidor aparece como **Não exigida**, e não como credencial autenticada.

**Diagnóstico:** **Diagnóstico STUN / UDP**. Mostra socket local, endereço/porta externos observados, detecção de tradução, autenticação, último evento e correlação de cada pedido com CWMP.

### Situação do Beta

Sondagens reais para `acs.example.com:3478/UDP` não receberam Binding Response válido no prazo de 10 segundos, em ambos os modos. Isso **não prova** que a porta está fechada: pode haver filtragem, configuração de servidor, política de origem ou outra limitação de caminho. Não foi informado NAT externo fictício.

Evidências:

```text
artifacts/stun-beta-3489.json
artifacts/stun-beta-5389.json
```

Sondagens temporárias reproduzíveis, sem habilitar o receptor permanente:

```bash
go run ./cmd/probe-stun rfc3489
go run ./cmd/probe-stun rfc5389
```

Para o teste completo, o administrador do ACS deve confirmar servidor/credenciais STUN e habilitar o envio do **UDP Connection Request TR-111**, a partir do IP correspondente ao servidor. Um STUN Binding sozinho não comprova entrega de Connection Request. O receptor de produção só é aberto quando STUN é habilitado nesta versão; não há receptor UDP independente com STUN desabilitado.

## Correlação: pedido recebido → sessão CWMP

Cada Connection Request autenticado recebe um identificador persistente e pode passar por:

```text
queued → session_started → cwmp_success
                         → cwmp_failed
```

`session_id` liga o pedido à sessão SOAP. Um evento aceito ou uma reconexão de XMPP/STUN não basta para marcar `cwmp_success`. Se o agente encerrar inesperadamente durante a sessão, uma linha pode permanecer `session_started`; confira o journal e o último horário, sem presumir sucesso.

Credenciais incorretas, remetente não autorizado e replay aparecem como eventos rejeitados, sem revelar payload ou senha. O log separa eventos de XMPP e de STUN. A fila limita pedidos aguardando execução.

## Verificação automatizada e física

Passaram os testes Go com detector de corrida e `go vet`, incluindo:

- SOAP Inform/GPV/SPV e faults com ACS local.
- XMPP TCP real com STARTTLS, certificado de teste confiável, SASL e resource bind.
- Senha de Connection Request incorreta rejeitada por IQ error.
- IQ XMPP válido provocando uma sessão CWMP confirmada.
- Certificado XMPP não confiável rejeitado antes da autenticação.
- STUN RFC 3489 e 5389 com challenge/autenticação em UDP real local.
- UDP Connection Request assinado provocando sessão CWMP, e assinatura incorreta/replay rejeitados.
- Vetor independente de integridade RFC 5769.
- Quatro páginas de diagnóstico e formulários XMPP/STUN em Chrome, incluindo layout móvel.

**O teste de AP físico por ACS controlado passou:** Inform, leitura, SPV, verificação física, nova leitura e SPV de restauração foram concluídos, com a WAN conectada. Evidência: `artifacts/cwmp-live-physical.json`. O teste reproduzível está no script abaixo. Ele pausa o agente periódico, usa um ACS HTTP somente em localhost, envia SOAP SPV para alterar o SSID real, consulta o novo valor e restaura o nome original, depois recupera o ACS Beta e o daemon:

```bash
python3 scripts/check-cwmp-live.py
```

**Esse script desconecta clientes do AP durante o teste.** Não execute enquanto algum cliente do laboratório depender da conexão. O relatório fica em `artifacts/cwmp-live-physical.json`.

Durante a atualização foi necessário corrigir o diretório de socket de resposta do hostapd_cli no helper e recarregar o helper junto ao binário novo; a instalação agora reinicia as unidades existentes para não deixar versões com schemas diferentes. Os faults de teste anteriores permanecem no histórico, separados dos sucessos posteriores.

## Operação e arquivos

```bash
systemctl status lab-router-api lab-router-helper lab-router-ap lab-router-dhcp lab-router-cwmp
journalctl -u lab-router-cwmp -u lab-router-helper -u lab-router-ap -u lab-router-dhcp -n 120 --no-pager
```

Código principal: `internal/traversal/`, `internal/cwmp/`, `internal/store/requests.go` e `web/src/MechanismDiagnosis.tsx`. Novas rotas de diagnóstico: `/api/tr069/diagnostics/basic`, `/periodic`, `/xmpp`, `/stun`; reconexão via POST `/api/tr069/xmpp/reconnect` e `/api/tr069/stun/reconnect`.

Ainda não implementados: TR-181, Get/SetParameterAttributes, DNS local, provisionamento automático de segundo rádio e criação persistente da interface AP após reboot. O listener HTTP Digest de Connection Request continua somente em localhost. A instalação não habilitou autostart nem alterou o trust store ou regras de firewall preexistentes.

## Referências

- [Broadband Forum TR-069, Annex G e K](https://www.broadband-forum.org/pdfs/tr-069-1-6-0.pdf)
- [RFC 3489](https://www.rfc-editor.org/rfc/rfc3489)
- [RFC 5389](https://www.rfc-editor.org/rfc/rfc5389)
- [RFC 5769 — vetores STUN](https://www.rfc-editor.org/rfc/rfc5769)
- [Mellium XMPP](https://github.com/mellium/xmpp)
