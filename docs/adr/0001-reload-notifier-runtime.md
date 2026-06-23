# ReloadNotifier — Recarregar Notifier em Runtime sem Reiniciar

**Contexto:** Após o usuário alterar configurações de notificação (Apprise URL, tags) via WebUI, o notifier Apprise em memória precisa refletir os novos valores. A alternativa óbvia seria reiniciar o processo — o que interromperia o servidor HTTP e qualquer deploy em andamento.

**Decisão:** Implementar `Server.ReloadNotifier()`, um método que recria o `notify.Notifier` sob `sync.Mutex`, atualiza o callback registrado no webhook handler e, se a URL estiver vazia, define o notifier como `nil` (desabilitado). Chamado exclusivamente pelo handler `SaveNotificationSettings` após persistir o YAML.

**Alternativas consideradas:**

- **Restart do processo**: interrompe o servidor e deploys em andamento. Rejeitada por ser destrutiva.
- **Config watcher (inotify)**: monitorar o arquivo YAML e recarregar automaticamente. Mais complexo, condição de corrida com saves concorrentes, e o usuário perderia feedback imediato de "configuração aplicada".
- **Sinal (SIGHUP)**: idiomático, mas exige tooling externo e não se integra bem com a WebUI.

**Consequência:** O notifier pode ficar momentaneamente `nil` durante o reload (janela de lock). Notificações disparadas nessa janela são perdidas — aceitável, pois o reload ocorre em microssegundos e o Apprise já é best-effort.

**Status:** accepted
