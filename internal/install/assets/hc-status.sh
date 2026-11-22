#!/bin/sh
# Forward the status line JSON (stdin) to the headcoach daemon. It carries what hooks do not:
# the size of the context window, the effort level and how much of the usage limits is spent.
# Call it from your status line script. It returns at once and never fails the status line.
body=$(cat)
(printf '%s' "$body" | curl -s -m 2 -o /dev/null -X POST \
  -H 'Content-Type: application/json' \
  -H "X-Config-Dir: ${CLAUDE_CONFIG_DIR:-}" \
  --data-binary @- "${HC_URL:-http://127.0.0.1:7337}/api/status") >/dev/null 2>&1 &
exit 0
