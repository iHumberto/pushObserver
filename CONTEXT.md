# pushObserver

Webhook-to-deploy automático para homelab: recebe webhooks de GitHub/Forgejo/GitLab, faz git pull, executa docker compose, e notifica via Apprise.

## Linguagem

**Hook**:
Uma configuração de webhook-to-deploy que mapeia um repositório Git para um pipeline de deploy. Cada hook tem ID único, URL do repo, branch, validação HMAC, e flags de notificação.
_Evitar_: Webhook config, listener, endpoint

**Apprise**:
Container de notificação multi-canal que expõe `POST /notify`. O pushObserver envia payloads JSON para este endpoint; o Apprise roteia para Discord, Telegram, ntfy, e 100+ outros serviços baseado em tags.
_Evitar_: Notification service, webhook forwarder

**HMAC**:
Validação criptográfica de assinatura de webhook. O remetente (GitHub/Forgejo) assina o payload com uma chave secreta; o pushObserver recalcula e compara. Tipos suportados: sha256, sha1, token, plain (dev only).
_Evitar_: Signature verification, hash check

**CSRF Token**:
Token aleatório gerado pelo servidor, enviado como cookie e campo hidden em formulários da WebUI. Na submissão, ambos devem bater — previne que sites maliciosos forjem requisições.
_Evitar_: Form token, anti-forgery token

**NotifyConfig**:
Configuração global de notificações no YAML. Contém `apprise_url` (endpoint do Apprise) e tags para eventos: `tag_success`, `tag_failure`, `tag_no_changes`.
_Evitar_: Notification settings, alert config

**NotifyHookConfig**:
Flags booleanas por hook que controlam quais eventos geram notificação: `on_success`, `on_failure`, `on_no_changes`. Permite silenciar notificações de hooks específicos.
_Evitar_: Per-hook notifications, hook alerts

**Deploy Engine**:
Pipeline que executa git pull no repositório do hook, detecta mudanças por serviço, e aciona docker compose build/up/restart apenas para serviços afetados.
_Evitar_: Deploy pipeline, deploy runner

**Service**:
Um serviço Docker Compose individual mapeado para um diretório no repositório. Cada serviço tem um `name`, `path` (relativo ao repo_dir), e `restart_trigger`.
_Evitar_: Container, compose service, app

**RestartTrigger**:
Política que define quando um serviço Docker é reiniciado após git pull: `default` (só se arquivos no path mudaram), `always` (sempre), `on-change` (mudanças em extensões customizadas).
_Evitar_: Restart policy, deploy trigger

**Token Bucket**:
Algoritmo de rate limiting que permite tráfego sustentado a uma taxa fixa com tolerância a bursts ocasionais. Cada IP tem um "balde" que enche a `requests_per_minute/60` tokens por segundo, com capacidade máxima de `burst` tokens.
_Evitar_: Rate limiter, throttle

**Scan**:
Descoberta automática de arquivos `docker-compose.yaml` dentro do `repo_dir` de um hook. Popula a lista de `services` quando nenhum serviço é definido explicitamente.
_Evitar_: Service discovery, compose detection

**Env Var Expansion**:
Substituição de padrões `${VAR}` e `${VAR:default}` no YAML de configuração por valores de variáveis de ambiente, realizada ANTES do parsing YAML. Permite que secrets (HMAC, Apprise URL) nunca estejam em plain text.
_Evitar_: Config templating, variable interpolation

## Relações

- Um **Hook** tem exatamente um **HMAC** config
- Um **Hook** referencia zero ou mais **Services**
- Um **Hook** tem um **NotifyHookConfig** que controla quais eventos notificam
- Um **Service** pertence a exatamente um **Hook**
- Um **Service** tem exatamente um **RestartTrigger**
- O **NotifyConfig** global define o **Apprise** endpoint e tags padrão
- O **Deploy Engine** é acionado por um **Hook** e opera sobre seus **Services**
- O **Token Bucket** protege todas as rotas HTTP, inclusive webhooks e WebUI

## Diálogo de exemplo

> **Dev:** "Quando um **Hook** recebe um webhook, como o **Deploy Engine** sabe quais **Services** reiniciar?"
> **Domain expert:** "O engine faz git pull no `repo_dir` do hook, detecta quais arquivos mudaram, e cruza com o `path` de cada **Service**. Se um serviço teve mudanças no seu diretório, ele é reconstruído e reiniciado. O **RestartTrigger** `always` ignora a detecção e sempre reinicia."
>
> **Dev:** "E a notificação? O **Apprise** é notificado para todos os hooks?"
> **Domain expert:** "Depende. O **NotifyConfig** global define o endpoint do Apprise. Mas cada hook tem seu próprio **NotifyHookConfig** — se `on_success` estiver desmarcado para um hook específico, deploys bem-sucedidos daquele hook não geram notificação, mesmo com Apprise configurado."

## Ambiguidades sinalizadas

- "webhook" foi usado para significar tanto o endpoint HTTP (`POST /hook/{id}`) quanto a configuração do hook em si — resolvido: **Hook** é a configuração; o endpoint é referido como "webhook endpoint" ou "POST /hook/{id}"
- "deploy" foi usado ambiguamente para o ato de fazer deploy E para o resultado do deploy — resolvido: o ato é "trigger deploy"; o resultado é **DeployResult** (struct retornada pelo engine)
