#!/bin/bash
set -e
[ -f /workspace/env-dir/env ] && export $(cat /workspace/env-dir/env | xargs)

# Pilot Protocol: bridge TCP from host socat to a local Unix socket
# so pilotctl can connect via PILOT_SOCKET=/run/pilot.sock
if [ -n "$PILOT_BRIDGE_PORT" ]; then
  socat UNIX-LISTEN:/tmp/pilot.sock,fork TCP:host.docker.internal:${PILOT_BRIDGE_PORT} &
  export PILOT_SOCKET=/tmp/pilot.sock
  # Brief wait for socket to be ready
  sleep 0.1
fi

cat > /tmp/input.json
cd /app && PYTHONPATH=/app uv run python -m src.main < /tmp/input.json
