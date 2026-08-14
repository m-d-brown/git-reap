#!/usr/bin/env bash
# Installs what the repository needs and nothing more: the Go version go.mod
# asks for, and pyte for scripts/capture.py. Re-run by hand after bumping
# go.mod:
#
#   ./.devcontainer/post-create.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# The workspace is bind-mounted from the host, so its files can carry a uid that
# does not exist here and git would refuse to touch the repository.
git config --global safe.directory "$PWD"

# go.mod is the only place the Go version lives; hand it to mise rather than
# restating it here.
mise use --global --yes "go@$(awk '$1 == "go" { print $2; exit }' go.mod)"

# capture.py imports pyte, so it has to be on an interpreter's path rather than
# installed as a tool. Its own venv keeps it off the system Python, and the
# Dockerfile has already put that venv first on PATH -- screenshot.sh calls
# python3 by name, and this is the python3 it should find.
uv venv --python 3.12 "$HOME/.venv"
uv pip install --python "$HOME/.venv/bin/python" pyte

# The same check screenshot.sh opens with, run now so a missing piece surfaces
# here rather than halfway through a screenshot.
for tool in go git fzf rsvg-convert python3; do
    command -v "$tool" > /dev/null || { echo "post-create: $tool is not installed" >&2; exit 1; }
done
python3 -c 'import pyte' || { echo "post-create: pyte is missing" >&2; exit 1; }

cat << 'EOF'

Ready.

    scripts/screenshot.sh    redraw docs/screenshot.png
    go test ./...            unit tests, plus the integration test
EOF