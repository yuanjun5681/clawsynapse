#!/bin/bash
# Wrapper around pilotctl that intercepts send-message commands
# and writes IPC event files so the host can display them in the frontend.

REAL_PILOTCTL=/usr/local/bin/pilotctl-bin
IPC_PILOT_DIR="/workspace/ipc/pilot-events"

# Execute the real command first
"$REAL_PILOTCTL" "$@"
EXIT_CODE=$?

# If the command failed, just return the exit code
if [ $EXIT_CODE -ne 0 ]; then
  exit $EXIT_CODE
fi

# Check if this was a send-message command
if [ "$1" = "send-message" ] && [ -n "$2" ]; then
  TARGET_NODE="$2"
  MSG_DATA=""
  MSG_TYPE="text"

  # Parse --data and --type flags
  shift 2
  while [ $# -gt 0 ]; do
    case "$1" in
      --data)
        MSG_DATA="$2"
        shift 2
        ;;
      --type)
        MSG_TYPE="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done

  if [ -n "$MSG_DATA" ]; then
    mkdir -p "$IPC_PILOT_DIR"
    chmod 777 "$IPC_PILOT_DIR" 2>/dev/null || true
    TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%S.000Z")
    RAND=$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')
    FILENAME="${IPC_PILOT_DIR}/$(date +%s%N | cut -c1-13)-${RAND}.json"

    # Use python for safe JSON serialization
    python3 -c "
import json, sys
obj = {
    'type': 'pilot_message_sent',
    'targetNode': sys.argv[1],
    'message': sys.argv[2],
    'messageType': sys.argv[3],
    'timestamp': sys.argv[4]
}
with open(sys.argv[5] + '.tmp', 'w') as f:
    json.dump(obj, f)
" "$TARGET_NODE" "$MSG_DATA" "$MSG_TYPE" "$TIMESTAMP" "$FILENAME"

    mv "${FILENAME}.tmp" "$FILENAME"
    chmod 666 "$FILENAME" 2>/dev/null || true
  fi
fi

exit $EXIT_CODE
