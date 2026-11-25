# Record docs/demo.gif with vhs (https://github.com/charmbracelet/vhs), on a board filled by demo.sh.
# Run it against a daemon and a tmux server of their own, never yours, from the repository root with
# ./hc built, outside tmux. The example tasks claim pane %0 of the default tmux server, which TMUX_TMPDIR
# moves; welcomed skips the first board's welcome:
#   unset TMUX && mkdir -p /tmp/hc && printf '[profiles]\nwork = "claude"\npersonal = "claude"\n' > /tmp/hc/config.toml
#   export HC_CONFIG=/tmp/hc/config.toml HC_STATE_DIR=/tmp/hc HC_PORT=7444
#   export HC_URL=http://127.0.0.1:7444 TMUX_TMPDIR=/tmp/hc
#   tmux new-session -d && ./hc serve &
#   scripts/demo.sh "$HC_URL" %0 /tmp/code /tmp/hc/tmux.sock && touch /tmp/hc/welcomed
#   vhs scripts/demo.tape
# The board needs about 210 columns for every column and the usage panel.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1840
Set Height 700
Set Padding 12
Set Framerate 20
Set TypingSpeed 0

Hide
Type "export PS1='$ ' && clear" Enter
# The review task ships while the board is open: it lights up as it moves to Done.
Type "(sleep 42; ./hc move 47a1c8e6 Done >/dev/null) & ./hc tui" Enter
Sleep 2s
Show

# The board: the usage panel, the key hints, a coordinator and the tasks it drives, the sub-agents.
Sleep 3s

# Enter a session, reply to the agent, then come back to the board: it keeps running.
Down@150ms 5
Sleep 0.8s
Type "a"
Sleep 2.5s
Type@60ms "Use waitFor on the cart total, not a sleep"
Sleep 0.5s
Enter
Sleep 3.5s
Ctrl+]
Sleep 1.5s

# The fuzzy filter: the cursor goes to the best fit, the arrows move it while you type.
Type "/"
Sleep 0.4s
Type@250ms "chk"
Sleep 1.5s
Up
Sleep 1.2s
Escape
Sleep 0.8s

# The coordinator view: what the coordinator drives, where each task stands, what waits on you.
Type "w"
Sleep 4s
Escape
Sleep 0.6s

# The coordinator's details: its handoff, its history and its session log.
Enter
Sleep 4s
Type "q"
Sleep 0.6s

# A pin: the task goes to the top with its sub-agent, marked ▲.
Type "g"
Down
Sleep 0.6s
Type "P"
Sleep 2.5s

# The review queue: the change, the tests, the reviewer's verdict, how it ships.
Type "R"
Sleep 3.5s
Escape
Sleep 0.6s

# The schedules.
Type "S"
Sleep 2.5s
Escape

# The review task moves to Done and lights up, then fades to grey.
Sleep 3.5s
Down@120ms 8
Sleep 2.5s

# A delete in 10 minutes, without a question: the ⏱ column counts down.
Type "X"
Sleep 3.5s
