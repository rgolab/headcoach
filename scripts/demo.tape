# Record docs/demo.gif with vhs (https://github.com/charmbracelet/vhs), on a board filled by demo.sh.
# Run it against a daemon and a tmux server of their own, never yours, from the repository root with
# ./hc built. The example tasks claim pane %0 of the default tmux server, which TMUX_TMPDIR moves:
#   export HC_CONFIG=/tmp/hc-demo/config.toml HC_STATE_DIR=/tmp/hc-demo/state HC_PORT=7444
#   export HC_URL=http://127.0.0.1:7444 TMUX_TMPDIR=/tmp/hc-demo
#   tmux new-session -d && ./hc serve &
#   scripts/demo.sh "$HC_URL" %0 /tmp/demo
#   vhs scripts/demo.tape
# The config needs two profiles, work and personal. The board needs about 180 columns for the usage panel.

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
Type "(sleep 17.5; ./hc move 47a1c8e6 Done >/dev/null) & ./hc tui" Enter
Sleep 2s
Show

# The board: the usage panel, the key hints, the tasks with their sub-agents.
Sleep 3.5s

# A task's details, history and session log.
Down 4
Sleep 0.6s
Enter
Sleep 3.5s
Type "q"
Sleep 0.8s

# The review queue: the change, the tests, the reviewer's verdict, how it ships.
Type "R"
Sleep 3.5s
Escape
Sleep 0.8s

# The schedules.
Type "S"
Sleep 2.5s
Escape

# The review task moves to Done and lights up, then fades to grey.
Sleep 7s
