#!/bin/bash
# Regenerate docs/screenshot.png and docs/screenshot-warning.png: build
# git-reap, build a demo repository, and run the real picker against it in a
# pty twice -- once posed on an ordinary unpushed commit, once posed on a
# branch whose commits live nowhere else -- drawing each screen it left
# behind.
#
#   usage: scripts/screenshot.sh
#
# Needs go, git, fzf, rsvg-convert (brew install librsvg / apt install
# librsvg2-bin), and python3 with pyte (pip install pyte).
set -euo pipefail

cd "$(dirname "$0")/.."

for tool in go git fzf rsvg-convert python3; do
  command -v "$tool" > /dev/null || { echo "screenshot: $tool is not installed" >&2; exit 1; }
done
python3 -c 'import pyte' 2> /dev/null || { echo "screenshot: pip install pyte" >&2; exit 1; }

WORKSPACE=$(mktemp -d)
trap 'rm -rf "$WORKSPACE"' EXIT

go build -o "$WORKSPACE/git-reap" .
bash scripts/demo-repo.sh "$WORKSPACE/demo"

# capture <name> <fzf-bind>: run the real picker posed by <fzf-bind> and draw
# the screen it left behind to docs/<name>.png. fzf takes its keystrokes from
# the tty, and driving that on a timer is a race, so every capture is posed
# with a load binding instead. `up` rather than `down` throughout, because
# fzf's default layout draws the first row at the bottom, and `load` rather
# than `start`, because start fires before the rows have arrived and there is
# nothing yet to move through. The list, the columns, and the preview are all
# the real thing; the SVG leans on whatever monospace font the viewer has, and
# the PNG -- what the README points at -- is drawn at 2x so it stays sharp on
# a dense display.
capture() {
  local name=$1 bind=$2
  FZF_DEFAULT_OPTS="--bind $bind" python3 scripts/capture.py \
    "$WORKSPACE/$name.svg" "$WORKSPACE/demo/checkout-service" "$WORKSPACE/git-reap"
  rsvg-convert -z 2 "$WORKSPACE/$name.svg" -o "docs/$name.png"
  echo "wrote docs/$name.png"
}

# The main screenshot: mark the two idle agent worktrees, walk up to
# fix/session-timeout, and mark that too, leaving the cursor on it so the
# preview pane has a branch to show -- the one that is in origin/main but
# ahead of its own remote branch, which is the row worth explaining.
capture screenshot 'load:toggle+up+toggle+up+up+up+up+up+up+up+toggle'

# The warning screenshot: walk up nine rows to spike/graphql-gateway -- an
# idea abandoned six months ago and never pushed anywhere -- and mark it,
# leaving the cursor there so the preview pane shows the red warning that
# deleting it drops commits that exist nowhere else.
capture screenshot-warning 'load:up+up+up+up+up+up+up+up+up+toggle'
