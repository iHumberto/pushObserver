# pushObserver

**Push on git → deploy on server in 1 minute. Zero scripts.**

pushObserver is a webhook receiver specialized in Git-to-Docker deployments.
It receives webhook POSTs from GitHub, Forgejo, Gitea, or GitLab, validates
HMAC signatures, pulls your repo, detects changed services, and runs
`docker compose up` for only what changed.

## How it works

```
git push → webhook POST → pushObserver → git pull → docker compose up → notified!
```

1. You `git push` to your repo
2. Your git platform sends a webhook POST to pushObserver's `/hook/:id`
3. pushObserver validates the HMAC signature
4. It pulls the repo, detects which services changed
5. Runs `docker compose up -d [--build]` for only the changed services
6. Notifies you via Apprise (Discord, Telegram, ntfy, ...)

## Quick Start

```bash
# Run with Docker Compose
docker compose up -d
```

## Configuration

Copy `push-observer.yaml` and customize your hooks. Secrets use `${ENV_VAR}` syntax.

```yaml
hooks:
  - id: homelab
    repo_url: "git@github.com:user/repo.git"
    repo_dir: "/home/pi/docker"
    branch: "main"
    hmac:
      type: sha256
      secret: "${HMAC_SECRET_HOMELAB}"
    services:
      - name: myapp
        path: "myapp"
        restart_trigger: default
```

## Supported Git Platforms

| Platform | HMAC Type | Header |
|----------|-----------|--------|
| GitHub | sha256 | X-Hub-Signature-256 |
| Forgejo | sha256 | X-Hub-Signature-256 |
| Gitea | sha256 | X-Hub-Signature-256 |
| GitLab | token | X-Gitlab-Token |

## Requirements

- Docker + Docker Compose
- A public or private Git repo
- (Optional) Apprise container for notifications

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).
