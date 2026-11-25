#!/bin/sh
# Fill a board with example tasks by replaying fake hook events, transcripts and status lines.
# For trying the UI, or recording it, without real sessions. Use a daemon and a tmux server of their
# own, never yours: the head of scripts/demo.tape shows how.
# Profiles named work and personal in that daemon's config split the tasks; they share one account's limits.
# Usage: scripts/demo.sh [daemon url] [live tmux pane id] [folder for the example projects] [board's tmux socket]
# With the socket of the daemon's own tmux server, the flaky test task runs there, in a stand-in session
# that a enters, that takes a reply, and that Ctrl-] leaves.
URL="${1:-http://127.0.0.1:7337}"
PANE="${2:-%0}"
BASE="${3:-$(mktemp -d)}"
SOCKET="$4"
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

# The stand-in session of the flaky test task: what a shows, made up like the rest. It looks like a
# Claude Code session, takes one reply, answers it, and keeps a spinner going until tmux closes it.
AP="$PANE" AS=""
if [ -n "$SOCKET" ]; then
  cat > "$LOGS/session.sh" <<'EOF'
exec 2>/dev/null
o='\033[38;5;173m' g='\033[38;5;245m' b='\033[1m' r='\033[0m'
W=$(printf '%51s' '' | sed 's/ /─/g') I=$(printf '%118s' '' | sed 's/ /─/g')
input() { # the input box, with the cursor in it
  printf "${g}╭${I}╮${r}\n${g}│${r} > \033[120G${g}│${r}\n${g}╰${I}╯${r}\n  ${g}? for shortcuts${r}\033[2A\r\033[5G"
}
spin() { # TEXT SECONDS: the spinner, three lines above the cursor
  n=0
  while :; do
    case $((n % 6)) in 0) c='·';; 1) c='✢';; 2) c='✳';; 3) c='✶';; 4) c='✻';; 5) c='✽';; esac
    printf "\033[s\033[3A\r${o}%s %s${r} ${g}(%ss · esc to interrupt)${r}\033[K\033[u" "$c" "$1" $(($2 + n / 8))
    n=$((n + 1)); sleep 0.125
  done
}
printf "${o}╭${W}╮${r}\n${o}│${r} ${o}✻${r} Welcome to ${b}Claude Code${r}!\033[53G${o}│${r}\n${o}│${r}\033[53G${o}│${r}\n"
printf "${o}│${r}   ${g}/help for help, /status for your current setup${r}\033[53G${o}│${r}\n${o}│${r}\033[53G${o}│${r}\n"
printf "${o}│${r}   ${g}cwd: ~/code/shop${r}\033[53G${o}│${r}\n${o}╰${W}╯${r}\n\n"
printf "${g}> Fix the flaky checkout test. It fails one run in five on CI.${r}\n\n"
printf "● I will look for fixed sleeps first.\n\n"
printf "\033[32m●${r} ${b}Explore${r}(Find other fixed sleeps in the tests)\n  ${g}⎿  Done (6 tool uses · 12.4k tokens · 21s)${r}\n\n"
printf "\033[32m●${r} ${b}Read${r}(tests/checkout.test.ts)\n  ${g}⎿  Read 42 lines${r}\n\n"
printf "● The test waits 200 ms for the cart to settle. On a slow runner it reads the total too early.\n"
printf "  I will wait for the total itself instead of a fixed time.\n\n"
printf "${g}●${r} ${b}Explore${r}(Trace where the cart total is computed)\n  ${g}⎿  Running…${r}\n\n\n\n"
input
spin 'Waiting on 1 sub-agent…' 38 &
read -r reply
kill $!
printf "\033[4A\r\033[J${g}> %s${r}\n\n" "$reply"
sleep 0.6
printf "● Right, a fixed sleep only hides the race. I will wait for the total itself.\n\n"
sleep 0.5
printf "\033[32m●${r} ${b}Update${r}(tests/checkout.test.ts)\n  ${g}⎿  Updated tests/checkout.test.ts with 1 addition and 1 removal${r}\n"
printf "       41    await cart.add(item)\n"
printf "       42 \033[48;5;52m-  await sleep(200) // let the cart settle  ${r}\n"
printf "       42 \033[48;5;22m+  await waitFor(() => cart.total() > 0)    ${r}\n"
printf "       43    expect(cart.total()).toBe(42)\n\n\n\n"
input
spin 'Running the checkout test 20 times…' 0 &
exec cat > /dev/null
EOF
  printf 'set -g status off\nset -g prefix None\nunbind-key -a\nbind-key -n C-] detach-client\n' > "$LOGS/tmux.conf"
  pane=$(tmux -S "$SOCKET" -f "$LOGS/tmux.conf" new-session -d -P -F '#{pane_id}' -x 200 -y 50 "sh $LOGS/session.sh")
  [ -n "$pane" ] && AP="$pane" AS="$SOCKET" # tmux may fail with no id and status 0: keep the live pane then
fi

post() { # $1: path  $2: config dir  $3: pane  $4: payload  [$5: tmux socket of the pane]
  curl -s -o /dev/null -X POST -H 'Content-Type: application/json' -H "X-Tmux-Pane: $3" \
    ${5:+-H "X-Tmux-Socket: $5"} -H "X-Config-Dir: $2" -d "$4" "$URL$1"
}
hook() { # $1: profile  $2: pane  $3: payload  [$4: tmux socket of the pane]
  post /api/hook "$HOME/.claude-$1" "$2" "$3" "$4"
}
# status PROFILE SESSION COST 5H% 5H_RESETS_IN 7D% 7D_RESETS_IN [EFFORT]: one status line update, times in seconds.
status() {
  post /api/status "$HOME/.claude-$1" "" '{"session_id":"'$2'","context_window":{"context_window_size":200000},
    "cost":{"total_cost_usd":'$3'},"effort":{"level":"'"${8:-medium}"'"},"rate_limits":{
    "five_hour":{"used_percentage":'$4',"resets_at":'$((NOW + $5))'},
    "seven_day":{"used_percentage":'$6',"resets_at":'$((NOW + $7))'}}}'
}
# say FILE ID TEXT IN OUT CACHE_READ CACHE_WRITE [MODEL]: one answer of the agent, with its usage.
say() {
  printf '{"type":"assistant","message":{"id":"%s","model":"%s","content":[{"type":"text","text":"%s"}],"usage":{"input_tokens":%s,"output_tokens":%s,"cache_read_input_tokens":%s,"cache_creation_input_tokens":%s}}}\n' \
    "$2" "${8:-claude-sonnet-4-5}" "$3" "$4" "$5" "$6" "$7" >> "$1"
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
start() { # PROFILE PANE SESSION PROJECT PROMPT [SOCKET]
  hook "$1" "$2" '{"hook_event_name":"UserPromptSubmit","session_id":"'$3'","cwd":"'"$BASE/$4"'","transcript_path":"'"$(log $3)"'","prompt":"'"$5"'"}' "$6"
}

A=3f2a9c1e-7b40-4d6a-9a53-0c1d8e5f2a11
B=9c47e2a0-51fd-4b8c-a7d2-3e6f0b1c8d94
C=e05b7d39-8a16-4c2f-b4e1-9d0a3f7c6e28
D=47a1c8e6-d0b2-4935-86fa-2b9e5c7d1f03
E=b81d4f2c-6e3a-4f19-8c75-1a9e0d2b7c46
F=1d6b3f70-2c84-4e5a-b19d-7a0e6c3f5b28

# Running: a coordinator that drives three of the tasks below, and keeps a handoff of where they stand.
ask "$(log $F)" "Ship the v2 checkout"
say "$(log $F)" f1 "Three tasks briefed: the flaky test, the duplicate email and the payment retries." 40 6100 210000 48000 claude-opus-4-5
say "$(log $F)" f2 "The reviewer approved the payment retries. They wait on your go before they merge." 12 900 230000 4000 claude-opus-4-5
start work "$PANE" $F shop "Ship the v2 checkout\nBrief one task per fix, then merge them in order."
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$F'","tool_name":"Bash","tool_input":{"command":"hc team self"}}'
status work $F 9.40 46 7900 31 262000 high

# Waiting: the main agent waits on a foreground sub-agent.
ask "$(log $A)" "Fix the flaky checkout test. It fails one run in five on CI."
say "$(log $A)" a1 "Let me look for fixed sleeps first." 12 1840 21000 38000
say "$(log $A)" a2 "The test waits 200 ms for the cart to settle." 8 2410 61000 9000
say "$(agent $A ag1)" s1 "Found 3 fixed sleeps." 20 960 18000 12000
say "$(agent $A ag2)" s2 "The total is computed in cart/summary.ts." 15 1320 26000 7000
start work "$AP" $A shop "Fix the flaky checkout test\nIt fails one run in five on CI." "$AS"
hook work "$AP" '{"hook_event_name":"PreToolUse","session_id":"'$A'","tool_name":"Agent","tool_input":{"subagent_type":"Explore","description":"Find other fixed sleeps in the tests"}}' "$AS"
hook work "$AP" '{"hook_event_name":"SubagentStart","session_id":"'$A'","agent_id":"ag1","agent_type":"Explore"}' "$AS"
hook work "$AP" '{"hook_event_name":"SubagentStop","session_id":"'$A'","agent_id":"ag1","agent_type":"Explore"}' "$AS"
hook work "$AP" '{"hook_event_name":"PostToolUse","session_id":"'$A'","tool_name":"Agent"}' "$AS"
hook work "$AP" '{"hook_event_name":"PreToolUse","session_id":"'$A'","tool_name":"Agent","tool_input":{"subagent_type":"Explore","description":"Trace where the cart total is computed"}}' "$AS"
hook work "$AP" '{"hook_event_name":"SubagentStart","session_id":"'$A'","agent_id":"ag2","agent_type":"Explore"}' "$AS"
status work $A 2.84 46 7900 31 262000 high

# Running, as a developer: a background sub-agent and a build at the same time.
ask "$(log $E)" "Upgrade the web framework to v5"
say "$(log $E)" e1 "Reading the migration guide." 30 3200 140000 52000
say "$(log $E)" e2 "Routes are updated, building now." 22 5100 128000 21000
say "$(agent $E ag3)" s3 "Updating the call sites." 18 2200 44000 16000
start personal "$PANE" $E web "Upgrade the web framework to v5"
hook personal "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$E'","tool_name":"Skill","tool_input":{"skill":"developer"}}'
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
status personal $C 0.58 46 7900 31 262000 low

# Review: done, with commits on a branch not pushed yet, a passing test run and a reviewer's verdict.
ask "$(log $D)" "api: retry on 503 from the payment provider"
say "$(log $D)" d1 "Added three retries with jittered backoff." 11 4300 120000 26000
run "$(log $D)" t1 "go test ./..." "ok  \\tapi/payment\\t0.412s"
say "$(agent $D ag4)" r1 "Verdict: approved, no blocking issues." 14 820 22000 6000
git -C "$BASE/api" checkout -q main # the task starts on main, so its change is the branch, also on a second run
start work "$PANE" $D api "api: retry on 503 from the payment provider"
git -C "$BASE/api" checkout -q retry-503 2>/dev/null || {
  git -C "$BASE/api" checkout -q -b retry-503
  for step in "Retry on 503" "Back off with jitter" "Test the retries"; do
    echo "$step" >> "$BASE/api/payment.txt" && gitd "$BASE/api" add payment.txt && gitd "$BASE/api" commit -q -m "payment: $step"
  done
}
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$D'","tool_name":"Agent","tool_input":{"subagent_type":"code-reviewer","description":"Review the retry change"}}'
hook work "$PANE" '{"hook_event_name":"SubagentStart","session_id":"'$D'","agent_id":"ag4","agent_type":"code-reviewer"}'
hook work "$PANE" '{"hook_event_name":"PreToolUse","session_id":"'$D'","agent_id":"ag4","tool_name":"Skill","tool_input":{"skill":"reviewer"}}'
hook work "$PANE" '{"hook_event_name":"SubagentStop","session_id":"'$D'","agent_id":"ag4","agent_type":"code-reviewer"}'
hook work "$PANE" '{"hook_event_name":"PostToolUse","session_id":"'$D'","tool_name":"Agent"}'
hook work "$PANE" '{"hook_event_name":"Stop","session_id":"'$D'","last_assistant_message":"Added three retries with jittered backoff. Three commits on retry-503, nothing pushed."}'
status work $D 3.95 46 7900 31 262000 high

# The coordinator drives three of them, and its handoff says where they stand.
for child in $A $B $D; do
  curl -s -o /dev/null -X PATCH -H 'Content-Type: application/json' -d '{"parent":"'$F'"}' "$URL/api/tasks/$child"
done
handoff=$(curl -s "$URL/api/tasks/$F/handoff" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
[ -n "$handoff" ] && mkdir -p "$(dirname "$handoff")" && cat > "$handoff" <<EOF
## Status
Three fixes for the v2 checkout, one task each. The payment retries are in review.

## Waiting
- on you: approve the payment retries before they merge
- on ${A%%-*}: the flaky test fix

## Next
Merge the payment retries, then the flaky test fix.

## Queue
- [x] Brief the flaky test fix
- [x] Brief the duplicate signup email
- [x] Brief the payment retries
- [ ] Merge them in order
- [ ] Draft the release notes
EOF

# Backlog: not started yet, on a small model.
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"prompt":"Draft the release notes for the next release","cwd":"'"$BASE/shop"'","model":"haiku","effort":"low"}' "$URL/api/tasks"

# A timer on the task with no pane, so running out ends no session, and two schedules that do not
# run before tomorrow morning.
curl -s -o /dev/null -X PATCH -H 'Content-Type: application/json' -d '{"timer":1500}' "$URL/api/tasks/$B"
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"title":"Inbox","prompt":"Sort the new issues and label them","at":"08:00","days":"weekdays","catch_up":true,"profile":"work","cwd":"'"$BASE/shop"'"}' "$URL/api/schedules"
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  -d '{"title":"Dependencies","prompt":"Update the dependencies and run the tests","at":"30 6 * * 1","timer":1800,"profile":"personal","cwd":"'"$BASE/web"'"}' "$URL/api/schedules"
echo "Seeded $URL, transcripts in $LOGS"
