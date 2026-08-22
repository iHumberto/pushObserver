# ADR-0002: Apprise como camada única de notificação

**Status:** accepted

O pushObserver não integra diretamente com Discord, Telegram, ntfy, ou qualquer outro serviço de notificação. Em vez disso, ele envia todas as notificações para um único endpoint HTTP — um container Apprise — que roteia para os backends configurados.

## Por que Apprise como proxy?

A alternativa seria integrar diretamente com cada serviço de notificação (webhooks do Discord, Bot API do Telegram, ntfy, etc.). Rejeitamos essa abordagem porque:

1. **Manutenção N²**: Cada novo serviço de notificação exigiria código novo no pushObserver (formatar payload, autenticar, tratar erros específicos). Com Apprise, o pushObserver tem **um** formato de payload e **um** endpoint.

2. **Configuração delegada ao operador**: O operador do homelab configura tokens, canais e webhooks no Apprise — o pushObserver não precisa saber se as notificações vão para Discord, Telegram, ou ambos. Isso separa a responsabilidade de deploy da responsabilidade de notificação.

3. **Tags como roteamento**: O Apprise suporta tags, permitindo que um único container notifique canais diferentes para eventos diferentes (`deploy-success` → #deploys, `deploy-failure` → #alerts + Telegram). O pushObserver só precisa escolher a tag certa.

## Alternativas consideradas

| Alternativa | Prós | Contras |
|---|---|---|
| Integração direta (Discord webhook, Telegram bot, etc.) | Sem dependência extra de container | Código N×M (N eventos × M serviços), manutenção pesada, secrets espalhados |
| ntfy como único backend | Mais simples que Apprise, Go library nativa | Limita o usuário a um serviço; Apprise suporta ntfy também |
| **Apprise HTTP API (escolhido)** | Um endpoint, 100+ backends, tags para roteamento | Dependência de container extra, latência adicional de um hop HTTP |

## Consequências

- **Positivas**: Superfície de código de notificação mínima (~180 linhas em `notify.go`), flexibilidade máxima para o operador (trocar de Discord para Telegram sem mexer no pushObserver), isolamento de falhas (Apprise offline não quebra deploys)
- **Negativas**: Dependência operacional de um segundo container, latência extra (~ms) do hop HTTP para o Apprise
- **Mitigação**: Notificação é best-effort — falha no Apprise é logged mas nunca interrompe o deploy. Timeout de 10s evita bloqueios longos
