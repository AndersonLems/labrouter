# Arquitetura e decisões

> Registro da versão inicial. A instalação física e os mecanismos XMPP/STUN foram implementados depois; consulte [a validação atualizada](TR069-XMPP-STUN.md) para o estado vigente.

## Processos

O mesmo executável contém três papéis principais:

- `api`: HTTP local, frontend embutido, autenticação, API REST, SSE e auditoria. Usuário sem root.
- `cwmp`: agente SOAP/CWMP independente, intervalo/retry, fila de eventos e Connection Request local. Mesmo usuário sem root.
- `helper`: root, socket Unix acessível ao grupo de serviço, operações fixas somente no laboratório.

Hostapd e dnsmasq permanecem processos de rede separados sob systemd. O nftables mantém tabelas próprias; não há daemon nftables paralelo que reescreva as regras globais.

SQLite usa WAL, busy timeout e uma conexão por processo. A configuração e os backups são cifrados com AES-GCM. A senha administrativa é bcrypt. Eventos/auditoria/status são separados de credenciais. API e CWMP serializam atualizações com flock sobre arquivo no diretório de dados.

## Código

| Diretório | Responsabilidade |
|---|---|
| `cmd/lab-router` | Entradas e ciclo de vida |
| `internal/api` | Rotas Chi, autenticação, CSRF, SSE e frontend embutido |
| `internal/model` | Tipos, defaults e validação comum |
| `internal/store` | SQLite, criptografia, eventos, auditoria, backups |
| `internal/service` | Configuração transacional compartilhada por web e CWMP |
| `internal/system` | Adaptadores de leitura Linux e diagnósticos |
| `internal/network` | Renderização hostapd/dnsmasq/nft e helper restrito |
| `internal/cwmp` | XML/SOAP, sessão HTTP, RPCs, parâmetros TR-098 e Connection Request |
| `web/src` | Painel React; mock visual explicitamente separado |
| `deploy` | Unidades systemd |
| `scripts` | Inspeção, navegador e sessão Beta opcional |

Não há dependência Docker em execução. Os serviços não têm API de shell. Argumentos são enviados a `exec.CommandContext`; o helper não aceita caminhos, interfaces ou comandos escolhidos pelo usuário.

## Fluxo de configuração

Web e SetParameterValues convergem em `ConfigService.Update`. O serviço adquire o lock entre processos, carrega o estado anterior, valida o conjunto, solicita aplicação ao helper quando houver mudança de rede e só então persiste a configuração cifrada. Falha do helper impede commit; falha de persistência depois da aplicação provoca tentativa de reaplicar o estado anterior.

O helper valida novamente os dados e o ambiente, gera arquivos próprios, valida dnsmasq/nft, instala atomicamente as tabelas do laboratório, aplica endereço/AP/DHCP e verifica o SSID/canal e estado hostapd. Ele salva o estado anterior e tenta rollback diante de falha. A aplicação de nftables usa um batch que remove/recria **somente as duas tabelas próprias**; não executa flush global. A atualização de hostapd implica interrupção dos clientes do laboratório.

O watchdog acompanha canal da WAN e mudanças de rotas. Trata perda/mudança do canal como motivo para parar o AP, sem reconfigurar a WAN. Nenhuma garantia de rollback físico foi declarada antes dos testes em hardware.

## CWMP e avaliação de reutilização

Foi avaliado EasyCwmp. O projeto declara dependências libubus, libuci, libubox, microxml, json-c e curl; integrá-lo exigiria uma camada OpenWrt/UCI ou ambiente separado. Para manter um único binário de aplicação e usar o mesmo serviço de configuração, foi escolhido um cliente nativo limitado a CWMP 1.0/SOAP 1.1. **Não é uma integração EasyCwmp nem uma alegação de implementação integral/certificada de TR-069.**

Referências consultadas:

- [Dependências oficiais EasyCwmp](https://github.com/pivasoftware/easycwmp/blob/master/ext/openwrt/build/Makefile)
- [CWMP TR-069 Amendment 6, Broadband Forum](https://www.broadband-forum.org/pdfs/tr-069-1-6-0.pdf)
- [Modelo InternetGatewayDevice TR-098, Broadband Forum](https://cwmp-data-models.broadband-forum.org/tr-069-1-0-0.html)
- [Schema CWMP 1.0, Broadband Forum](https://github.com/BroadbandForum/cwmp-data-models/blob/master/cwmp-1-0.xsd)

O cliente usa `encoding/xml` e `net/http`, com reutilização da biblioteca Go `github.com/icholy/digest` para autenticação HTTP Digest do ACS. Suporta Basic, cookies da sessão e verificação TLS padrão. Redirects são recusados. Não registra bodies SOAP, senhas ou headers Authorization.

Inform abre a sessão; InformResponse é validado; POST vazio permite ao ACS entregar RPCs; cada resposta preserva cwmp:ID; HTTP vazio/204 encerra a sessão. Uma resposta HTTP 200 com HTML não conta como sucesso. Métodos desconhecidos recebem fault 9000. STUN e XMPP não têm implementações compatíveis e não podem ser habilitados.

O modelo TR-098 é um subconjunto explícito. Extensões `X_LAB_*` são identificadas como de laboratório. TR-181 não é publicado como equivalente. GetRPCMethods anuncia somente métodos implementados. FactoryReset depende de política local e preserva ACS/identidade; Reboot afeta apenas serviços virtuais.

## API

As rotas solicitadas estão em `internal/api/api.go`. GET consulta; PUT grava módulos; POST executa ações. Destaques adicionais:

- `/api/config`: leitura redigida/escrita do conjunto, para atualizar LAN e DHCP na mesma transação.
- `/api/events`: SSE autenticado.
- `/api/auth/login`, `/session`, `/logout`, `/password`: autenticação e gestão da senha única.
- `/api/tr069/diagnostics`: DNS/TCP; não confundir com `/api/tr069/inform`.
- `/api/backups/{id}/restore`: restauração validada.
- `/api/system/reboot`: reinício lógico.
- `/api/tr069/factory-reset-policy`: habilitação local explícita de FactoryReset remoto.

O painel permanece em loopback. Não foi implementada exposição pela LAN com TLS/allowlist; tentar usar bind externo é recusado. Para acesso remoto de administração, o administrador pode avaliar um túnel SSH autenticado conforme a política de sua rede, sem expor o painel na WAN.

## Limites de segurança operacional

A cifra em repouso protege cópias do banco sem a chave, mas não protege contra root nem contra um processo comprometido executando com o mesmo usuário que lê a chave. O diretório de rede é root e não gravável pelo backend. O socket permite ao usuário de serviço pedir as operações de rede previamente limitadas; não concede root genérico.

Não há proteção universal contra bugs do driver Wi-Fi nem contra políticas corporativas desconhecidas. A validação ao vivo, as regras externas e os testes de isolamento precisam ser concluídos antes de considerar o laboratório operacional. Consulte a matriz de validação para os recursos ainda incompletos.
