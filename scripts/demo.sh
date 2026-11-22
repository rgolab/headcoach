#!/bin/sh
# Fill a board with example tasks by replaying fake hook events, transcripts and status lines.
# For trying the UI, or recording it, without real sessions. Use a daemon and a tmux server of their
# own, never yours: the head of scripts/demo.tape shows how.
# Profiles named work and personal in that daemon's config split the tasks; they share one account's limits.
# Usage: scripts/demo.sh [daemon url] [live tmux pane id] [folder for the example projects]
URL="${1:-http://127.0.0.1:7337}"
PANE="${2:-%0}"
BASE="${3:-$(mktemp -d)}"
gitd() { # DIR ARGS...: git as a demo author
  dir="$1"; shift
  git -C "$dir" -c user.name=demo -c user.email=demo@example.com "$@"
}
for project in shop api web; do # small git repos pushed to a local origin, so each task shows a repo and a branch
  [ -d "$BASE/$project/.git" ] || { mkdir -p "$BASE/$project" && git -C "$BASE/$project" init -q -b main &&
    gitd "$BASE/$project" commit -q --allow-empty -m init &&
    git init -q --bare "$BASE/.origin/$project.git" && git -C "$BASE/$project" remote add origin "$BASE/.origin/$project.git" &&
    git -C "$BASE/$project" push -q -u origin main; }
done
LOGS="$(mktemp -d)"
NOW=$(date +%s)

post() { # $1: path  $2: config dir  $3: pane  $4: payload
  curl -s -o /dev/null -X POST -H 'Content-Type: application/json' -H "X-Tmux-Pane: $3" \
    -H "X-Config-Dir: $2" -d "$4" "$URL$1"
}
hook() { # $1: profile  $2: pane  $3: payload
  post /api/hook "$HOME/.claude-$1" "$2" "$3"
}
# status PROFILE SESSION COST 5H% 5H_RESETS_IN 7D% 7D_RESETS_IN: one status line update, times in seconds.
status() {
  post /api/status "$HOME/.claude-$1" "" '{"session_id":"'$2'","context_window":{"context_window_size":200000},
    "cost":{"total_cost_usd":'$3'},"rate_limits":{
    "five_hour":{"used_percentage":'$4',"resets_at":'$((NOW + $5))'},
    "seven_day":{"used_percentage":'$6',"resets_at":'$((NOW + $7))'}}}'
}
# say FILE ID TEXT IN OUT CACHE_READ CACHE_WRITE: one answer of the agent, with its usage.
say() {
  printf '{"type":"assistant","message":{"id":"%s","model":"claude-sonnet-4-5","content":[{"type":"text","text":"%s"}],"usage":{"input_tokens":%s,"output_tokens":%s,"cache_read_input_tokens":%s,"cache_creation_input_tokens":%s}}}\n' \
    "$2" "$3" "$4" "$5" "$6" "$7" >> "$1"
}
# run FILE ID COMMAND OUTPUT: one shell command of the agent, and what it printed.
run() {
  at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  printf '{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","id":"%s","name":"Bash","input":{"command":"%s"}}]}}\n' "$at" "$2" "$3" >> "$1"
  printf '{"type":"user","timestamp":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"%s","content":"%s"}]}}\n' "$at" "$2" "$4" >> "$1"
}
ask() { # FILE TEXT: one prompt of the user
  printf '{"type":"user","message":{"content":"%s"}}\n' "$2" >> "$1"
}
log() { # SESSION: the transcript path of a session
  echo "$LOGS/$1.jsonl"
}
agent() { # SESSION AGENT: the transcript path of a sub-agent
  mkdir -p "$LOGS/$1/subagents" && echo "$LOGS/$1/subagents/agent-$2.jsonl"
}
start() { # PROFILE PANE SESSION PROJECT PROMPT
  hook "$1" "$2" '{"hook_event_name":"UserPromptSubmit","session_id":"'$3'","cwd":"'"$BASE/$4"'","transcript_path":"'"$(log $3)"'","prompt":"'"$5"'"}'
}

A=3f2a9c1e-7b40-4d6a-9a53-0c1d8e5f2a11
B=9c47e2a0-51fd-4b8c-a7d2-3e6f0b1c8d94
C=e05b7d39-8a16-4c2f-b4e1-9d0a3f7c6e28
D=47a1c8e6-d0b2-4935-86fa-2b9e5c7d1f03
E=b81d4f2c-6e3a-4f19-8c75-1a9e0d2b7c46

# Waiting: the main agent waits on a foreground sub-agent.
ask "$(log $A)" "Fix the flaky checkout test. It fails one run in five on CI."
say "$(log $A)" a1 "Let me look for fixed sleeps first." 12 1840 21000 38000
say "$(log $A)" a2 "The test waits 200 ms for the cart to settle." 8 2410 61000 9000
say "$(agent $A ag1)" s1 "Found 3 fixed sleeps." 20 960 18000 12000
say "$(agent $A ag2)" s2 "The total is computed in cart/summary.ts." 15 1320 26000 7000
start work "$PANE" $A shop "Fix the flaky checkout test\nIt fails one run in five on CI."
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$A'","tool_name":"Agent","tool_input":{"subagent_type":"Explore","description":"Find other fixed sleeps in the tests"}}'
hook work "$PANE" '{"hook_event_name":"SubagentStart","session_id":"'$A'","agent_id":"ag1","agent_type":"Explore"}'
hook work "$PANE" '{"hook_event_name":"SubagentStop","session_id":"'$A'","agent_id":"ag1","agent_type":"Explore"}'
hook work "$PANE" '{"hook_event_name":"PostToolUse","session_id":"'$A'","tool_name":"Agent"}'
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$A'","tool_name":"Agent","tool_input":{"subagent_type":"Explore","description":"Trace where the cart total is computed"}}'
hook work "$PANE" '{"hook_event_name":"SubagentStart","session_id":"'$A'","agent_id":"ag2","agent_type":"Explore"}'
status work $A 2.84 46 7900 31 262000

# Running: a background sub-agent and a build at the same time.
ask "$(log $E)" "Upgrade the web framework to v5"
say "$(log $E)" e1 "Reading the migration guide." 30 3200 140000 52000
say "$(log $E)" e2 "Routes are updated, building now." 22 5100 128000 21000
say "$(agent $E ag3)" s3 "Updating the call sites." 18 2200 44000 16000
start personal "$PANE" $E web "Upgrade the web framework to v5"
hook personal "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$E'","tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","description":"Update the call sites of the router","run_in_background":true}}'
hook personal "$PANE" '{"hook_event_name":"SubagentStart","session_id":"'$E'","agent_id":"ag3","agent_type":"general-purpose"}'
hook personal "$PANE" '{"hook_event_name":"PostToolUse","session_id":"'$E'","tool_name":"Agent"}'
hook personal "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$E'","tool_name":"Bash","tool_input":{"command":"npm run build"}}'
status personal $E 6.12 46 7900 31 262000

# Needs me: a question.
ask "$(log $B)" "The signup email is sent twice after a restart"
say "$(log $B)" b1 "The sent flag is saved after the email goes out." 9 2900 88000 14000
start work "%none" $B shop "The signup email is sent twice after a restart"
hook work "%none" '{"hook_event_name":"Stop","session_id":"'$B'","last_assistant_message":"The sent flag is saved after the email goes out.\n\nSave the flag before sending, or keep it after and dedupe on retry?"}'
status work $B 1.37 46 7900 31 262000

# Needs me: a permission.
ask "$(log $C)" "Add dark mode to the settings page"
say "$(log $C)" c1 "I will add a theme toggle." 6 1100 23000 8000
start personal "$PANE" $C web "Add dark mode to the settings page"
hook personal "$PANE" '{"hook_event_name":"Notification","session_id":"'$C'","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}'
status personal $C 0.58 46 7900 31 262000

# Review: done, with commits on a branch not pushed yet, a passing test run and a reviewer's verdict.
ask "$(log $D)" "api: retry on 503 from the payment provider"
say "$(log $D)" d1 "Added three retries with jittered backoff." 11 4300 120000 26000
run "$(log $D)" t1 "go test ./..." "ok  \\tapi/payment\\t0.412s"
say "$(agent $D ag4)" r1 "Verdict: approved, no blocking issues." 14 820 22000 6000
start work "$PANE" $D api "api: retry on 503 from the payment provider"
git -C "$BASE/api" rev-parse -q --verify retry-503 >/dev/null || {
  git -C "$BASE/api" checkout -q -b retry-503
  for step in "Retry on 503" "Back off with jitter" "Test the retries"; do
    echo "$step" >> "$BASE/api/payment.txt" && gitd "$BASE/api" add payment.txt && gitd "$BASE/api" commit -q -m "payment: $step"
  done
}
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$D'","tool_name":"Agent","tool_input":{"subagent_type":"code-reviewer","description":"Review the retry change"}}'
hook work "$PANE" '{"hook_event_name":"SubagentStart","session_id":"'$D'","agent_id":"ag4","agent_type":"code-reviewer"}'
hook work "$PANE" '{"hook_event_name":"SubagentStop","session_id":"'$D'","agent_id":"ag4","agent_type":"code-reviewer"}'
hook work "$PANE" '{"hook_event_name":"PostToolUse","session_id":"'$D'","tool_name":"Agent"}'
hook work "$PANE" '{"hook_event_name":"Stop","session_id":"'$D'","last_assistant_message":"Added three retries with jittered backoff. Three commits on retry-503, nothing pushed."}'
status work $D 3.95 46 7900 31 262000

# Backlog: not started yet.
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"prompt":"Draft the release notes for the next release","cwd":"'"$BASE/shop"'"}' "$URL/api/tasks"

# A timer on the task with no pane, so running out ends no session, and two schedules that do not
# run before tomorrow morning.
curl -s -o /dev/null -X PATCH -H 'Content-Type: application/json' -d '{"timer":1500}' "$URL/api/tasks/$B"
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"title":"Inbox","prompt":"Sort the new issues and label them","at":"08:00","days":"weekdays","catch_up":true,"profile":"work","cwd":"'"$BASE/shop"'"}' "$URL/api/schedules"
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"title":"Dependencies","prompt":"Update the dependencies and run the tests","at":"30 6 * * 1","timer":1800,"profile":"personal","cwd":"'"$BASE/web"'"}' "$URL/api/schedules"
echo "Seeded $URL, transcripts in $LOGS"
