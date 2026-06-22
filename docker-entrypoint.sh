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
if [ ! -w "$(dirname "$CONFIG")" ]; then
    echo "ERROR: $(dirname "$CONFIG") is not writable by $(whoami) (UID $(id -u))" >&2
    echo "Fix: ensure the directory is owned by UID $(id -u), e.g.:" >&2
    echo "  docker run --user root <image> chown -R $(id -u):$(id -g) $(dirname "$CONFIG")" >&2
    echo "  or on host volume: chown -R $(id -u):$(id -g) /path/to/.config" >&2
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
