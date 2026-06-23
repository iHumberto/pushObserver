#!/bin/sh
# pushObserver Docker entrypoint
# On first run, auto-creates a minimal push-observer.yaml from the bundled default.
# Then execs the push-observer binary.

set -e

CONFIG="/home/webhook/.config/push-observer.yaml"
DEFAULT="/home/webhook/push-observer.yaml.default"

# Ensure config directory exists
mkdir -p "$(dirname "$CONFIG")"

# Ensure config directory is writable by the webhook user.
# chown doesn't work here (we run as non-root), so we detect and fail early
# with a clear message instead of a cryptic "Permission denied".
#
# Common cause: bind mount created by Docker daemon (running as root) —
# the host directory gets root:root ownership, overriding the Dockerfile's chown.
# Fix: chown the HOST directory before starting the container.
CONFIG_DIR="$(dirname "$CONFIG")"
if [ ! -w "$CONFIG_DIR" ]; then
    OWNER=$(stat -c '%U:%G' "$CONFIG_DIR" 2>/dev/null || echo "unknown")
    PERMS=$(stat -c '%a' "$CONFIG_DIR" 2>/dev/null || echo "???")
    echo "ERROR: $CONFIG_DIR is not writable" >&2
    echo "  Current owner: $OWNER, permissions: $PERMS" >&2
    echo "  Running as:    $(whoami) (UID $(id -u), GID $(id -g))" >&2
    echo "" >&2
    echo "This is usually caused by a bind mount where the host directory" >&2
    echo "was created by the Docker daemon with root ownership." >&2
    echo "" >&2
    echo "Fix on the HOST (not inside the container):" >&2
    echo "  sudo chown -R $(id -u):$(id -g) /path/to/host/config/dir" >&2
    exit 1
fi

if [ ! -f "$CONFIG" ]; then
    echo "==> No config found at $CONFIG"
    if [ -f "$DEFAULT" ]; then
        echo "==> Creating default config from bundled template..."
        cp "$DEFAULT" "$CONFIG"
    else
        echo "==> Creating minimal config..."
        cat > "$CONFIG" << 'YAMLEOF'
# pushObserver — auto-generated minimal config
server:
  port: 9090
  host: "0.0.0.0"
hooks: []
notifications:
  apprise_url: ""
logging:
  level: "info"
  format: "json"
rate_limit:
  enabled: true
  requests_per_minute: 30
  burst: 5
YAMLEOF
    fi
    echo "==> Config created. Edit $CONFIG to add hooks."
fi

exec push-observer "$@"
