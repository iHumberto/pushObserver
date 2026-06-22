#!/bin/sh
# pushObserver Docker entrypoint
# On first run, auto-creates a minimal push-observer.yaml from the bundled default.
# Then execs the push-observer binary.

set -e

CONFIG="/home/webhook/.config/push-observer.yaml"
DEFAULT="/home/webhook/push-observer.yaml.default"

# Ensure config directory exists
mkdir -p "$(dirname "$CONFIG")"

# Ensure config directory is writable by webhook user
# Defends against: (a) stale Docker layer cache with root-owned .config
# (b) volume mounts from host with root ownership
chown webhook:webhook "$(dirname "$CONFIG")" 2>/dev/null || true

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
