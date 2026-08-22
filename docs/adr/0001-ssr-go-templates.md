# ADR-0001: Server-side rendering com Go templates

**Status:** accepted

O pushObserver renderiza todas as páginas da WebUI no servidor, usando templates Go (`html/template`) com CSS vanilla e zero JavaScript de build. Não há SPA, não há React/Vue, não há bundler.

## Por que SSR com Go templates?

A alternativa natural para uma dashboard de gerenciamento seria um SPA (React/Vue) com API REST. Rejeitamos essa abordagem por três motivos:

1. **Público-alvo é homelab**: O usuário é um operador solo que acessa a dashboard de um navegador na rede local. A complexidade de um SPA (build step, node_modules, roteamento client-side, state management) não se justifica.

2. **Zero dependências de build**: Templates Go são compilados junto com o binário (`embed`). Não há `npm install`, `webpack`, ou `node_modules`. O binário final é autossuficiente — sobe com `docker compose up` e a UI está pronta.

3. **CSRF e segurança simplificados**: Com SSR, o token CSRF é injetado no HTML durante a renderização. Não há API de autenticação, não há JWT, não há CORS complexo — o modelo de segurança é o tradicional cookie + hidden field, adequado para single-user.

## Alternativas consideradas

| Alternativa | Prós | Contras |
|---|---|---|
| SPA (React/Vue) + API REST | Separação clara front/back, UX rica | Build step, node_modules, complexidade de auth (JWT, CORS), bundle size |
| HTMX + Go templates | Menos JavaScript, partial updates | Menos maduro que templates puros, curva de aprendizado adicional |
| **Go templates SSR (escolhido)** | Zero JS build, binário único, CSRF simples | UX menos dinâmica, full page reloads |

## Consequências

- **Positivas**: Deploy simplificado (um binário), menos superfície de ataque (sem API auth), manutenção mais barata
- **Negativas**: Experiência menos fluida (full page reload em cada ação), sem atualização em tempo real do status de deploy
- **Mitigação para UX**: CSS transitions e feedback visual (mensagens de sucesso/erro via query params) compensam parcialmente a falta de reatividade
