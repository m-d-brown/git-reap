#!/bin/bash
# Regenerate docs/screenshot.png: build git-reap, build a demo repository,
# run the real picker against it in a pty, and draw the screen it left.
#
#   usage: scripts/screenshot.sh
#
# Needs go, git, fzf, rsvg-convert (brew install librsvg / apt install
# librsvg2-bin), and python3 with pyte (pip install pyte).
set -euo pipefail

cd "$(dirname "$0")/.."
OUTPUT=docs/screenshot.png

for tool in go git fzf rsvg-convert python3; do
  command -v "$tool" > /dev/null || { echo "screenshot: $tool is not installed" >&2; exit 1; }
done
python3 -c 'import pyte' 2> /dev/null || { echo "screenshot: pip install pyte" >&2; exit 1; }

WORKSPACE=$(mktemp -d)
trap 'rm -rf "$WORKSPACE"' EXIT

go build -o "$WORKSPACE/git-reap" .
bash scripts/demo-repo.sh "$WORKSPACE/demo"

# fzf takes its keystrokes from the tty, and driving that on a timer is a race,
# so the picker is posed with a binding instead: mark the two idle agent
# worktrees, walk up to fix/session-timeout, and mark that too, leaving the
# cursor on it so the preview pane has a branch to show -- the one that is in
# origin/main but ahead of its own remote branch, which is the row worth
# explaining. `up` rather than `down`, because fzf's default layout draws the
# first row at the bottom, and `load` rather than `start`, because start fires
# before the rows have arrived and there is nothing yet to move through. The
# list, the columns, and the preview are all the real thing.
export FZF_DEFAULT_OPTS='--bind load:toggle+up+toggle+up+up+up+up+up+up+up+toggle'

python3 scripts/capture.py "$WORKSPACE/screenshot.svg" "$WORKSPACE/demo/checkout-service" \
  "$WORKSPACE/git-reap"

# The SVG leans on whatever monospace font the viewer has; the PNG is what the
# README points at, at 2x so it stays sharp on a dense display.
rsvg-convert -z 2 "$WORKSPACE/screenshot.svg" -o "$OUTPUT"
echo "wrote $OUTPUT"
