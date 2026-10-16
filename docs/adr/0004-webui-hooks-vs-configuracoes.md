# ADR-0004: Divisão da WebUI entre Hooks e Configurações

**Status:** accepted

A WebUI do pushObserver separa estritamente **configuração do app** de **configuração de hook**. A tela de Configurações cuida apenas do que vale para a instância inteira; tudo que pertence a um hook específico vive na tela de Hooks, dentro de um popup de criação/edição. O scan de repositório é a fonte dos gatilhos de deploy e tem dois pontos de entrada: o popup e um botão por linha na lista de hooks.

## Por que separar?

O protótipo anterior permitia criar e configurar hooks **nas duas telas**. Isso gerava três problemas:

1. **Duplicidade de criação**: a tela de Configurações tinha um seletor de hook com cards de Deploy e Serviços, exatamente a mesma configuração que o popup da tela de Hooks oferecia. Duas telas que escrevem o mesmo campo são duas telas que podem divergir — e duas implementações de validação para manter.

2. **Escopo ambíguo para o operador**: ao abrir Configurações, o usuário não sabia se estava mudando algo global ou algo do hook selecionado. `compose.build` e `compose.cleanup` são do hook; `rate_limit` e `notify` são do app. Misturar os dois num mesmo formulário convida a erro operacional num sistema que faz deploy em produção.

3. **Estado vazio artificial**: existia uma tela "Configurações — primeiro acesso (sem hooks)" só porque os cards de Deploy/Serviços não tinham o que mostrar. Com a divisão, Configurações nunca depende da existência de um hook, e o estado vazio é responsabilidade da Home.

## Onde cada coisa fica

| Escopo | Tela | Conteúdo |
|---|---|---|
| App (global) | **Configurações** | Limite de requisições, acesso (senha/sessão), registro de logs, notificações globais (Apprise URL, tags) |
| Hook | **Hooks → popup** | Identidade do repo (id, branch, URL, diretório, chave SSH), assinatura HMAC, Compose (`build`, `cleanup`), Pipeline (`timeout`, `retry`), serviços, gatilhos de deploy, notificações do hook |

O popup é **coluna única com rolagem** (viewport clipado + barra de rolagem), com cabeçalho e botão de ação fixos. Rejeitamos duas colunas (quebra em telas estreitas e espalha campos correlatos) e abas (esconde campos obrigatórios atrás de um clique, favorecendo hook salvo pela metade).

## Scan como fonte dos gatilhos

O campo livre "Extensões que forçam restart" foi substituído por **chips selecionáveis alimentados pelo scan**. O scan percorre o repositório e devolve:

- os serviços encontrados no `docker-compose.yaml`, com checkbox para escolher quais o hook gerencia;
- os tipos de arquivo presentes, com contagem (`.env (4)`, `.py (31)`, `.ts (58)`, …), para o operador marcar quais disparam deploy.

Isso elimina a classe de erro mais comum do campo livre: digitar uma extensão que não existe no repo (`.tsx` num projeto só de `.ts`) e ficar com um `restart_trigger` por extensão que nunca dispara. O que o usuário pode escolher é exatamente o que existe.

Dois pontos de entrada, porque são dois momentos diferentes:

- **No popup** ("🔍 Escanear repositório"): durante a criação, quando o usuário ainda não sabe o que tem no repo — o resultado preenche serviços e gatilhos no próprio formulário.
- **Na lista de hooks** (botão 🔍 por linha, abre um **modal de scan dedicado**): depois, quando o repo mudou e o operador quer reconciliar sem reabrir o formulário inteiro. Reaproveitar o popup de edição aqui obrigaria a carregar e revalidar todos os campos para uma ação de leitura.

## Alternativas consideradas

| Alternativa | Prós | Contras |
|---|---|---|
| Manter hooks nas duas telas | Nenhuma migração de protótipo | Duplicidade de formulário e validação, escopo ambíguo, estado vazio artificial |
| Tudo (app + hooks) só em Configurações | Uma tela única | Lista de hooks perde a função; formulário gigante com escopos misturados |
| Página dedicada por hook (sem popup) | Mais espaço, URL por hook | Navegação extra para uma ação curta; perde o contexto da lista |
| **Configurações = app, Hooks = popup rolável (escolhido)** | Escopo explícito, um único lugar que escreve cada campo | Popup alto exige rolagem interna |

## Consequências

- **Positivas**: cada campo do `push-observer.yaml` tem exatamente um lugar na UI que o escreve; o scan vira pré-requisito natural dos gatilhos; Configurações funciona com zero hooks.
- **Negativas**: o popup concentra muito conteúdo e depende de rolagem interna; o scan precisa ser rápido o suficiente para ser chamado duas vezes por fluxos diferentes.
- **Pendente**: `internal/server/templates/dashboard.html` e `internal/server/ui.go` ainda refletem a divisão antiga. Esta ADR registra a decisão de design (protótipo no Penpot); a implementação é trabalho subsequente.
