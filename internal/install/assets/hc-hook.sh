#!/bin/sh
# Forward one Claude Code hook payload (stdin) to the headcoach daemon.
# Fire-and-forget: it never blocks or fails the session, even when the daemon is down.
# HC_IGNORE marks a helper run of Claude Code that must not show up as a task.
[ -n "${HC_IGNORE:-}" ] && exit 0
curl -s -m 2 -o /dev/null -X POST \
  -H 'Content-Type: application/json' \
  -H "X-Tmux-Pane: ${TMUX_PANE:-}" \
  -H "X-Tmux-Socket: ${TMUX%%,*}" \
  -H "X-Herdr-Pane: ${HERDR_PANE_ID:-}" \
  -H "X-Herdr-Socket: ${HERDR_SOCKET_PATH:-}" \
  -H "X-Config-Dir: ${CLAUDE_CONFIG_DIR:-}" \
  -H "X-HC-Task: ${HC_TASK:-}" \
  --data-binary @- "${HC_URL:-http://127.0.0.1:7337}/api/hook" 2>/dev/null
exit 0
