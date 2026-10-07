#!/usr/bin/env bash
# Installs what the repository needs and nothing more: the Go version go.mod
# asks for, the tools mise.toml pins, and pyte for scripts/capture.py. Re-run by
# hand after bumping go.mod or mise.toml:
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

# Everything mise.toml pins -- fzf, today -- fetched now rather than on the
# first screenshot, so a redraw does not stop to download a picker.
mise install

# And mirrored into the global config, so the pinned fzf answers from anywhere
# in the container rather than only from the repository root. mise.toml does not
# reach the demo repositories under /tmp, and those are exactly where you stand
# when you try the picker by hand -- with no distro fzf installed any more,
# without this it would not be found at all. The version is read back out of
# mise.toml rather than restated, the same way the Go line reads go.mod.
# scripts/screenshot.sh does not rely on this: it resolves fzf for itself, so a
# machine that never ran this script still draws the right picture.
mise use --global --yes "fzf@$(mise ls --current fzf | awk '{print $2}')"

# capture.py imports pyte, so it has to be on an interpreter's path rather than
# installed as a tool. Its own venv keeps it off the system Python, and the
# Dockerfile has already put that venv first on PATH -- screenshot.sh calls
# python3 by name, and this is the python3 it should find.
uv venv --python 3.12 "$HOME/.venv"
uv pip install --python "$HOME/.venv/bin/python" pyte

# The same check screenshot.sh opens with, run now so a missing piece surfaces
# here rather than halfway through a screenshot.
for tool in go git rsvg-convert python3; do
    command -v "$tool" > /dev/null || { echo "post-create: $tool is not installed" >&2; exit 1; }
done
# fzf is asked for through mise rather than PATH, because that is how
# screenshot.sh gets it and a shim here would answer with the wrong one.
mise which fzf > /dev/null || { echo "post-create: fzf is missing; check mise.toml" >&2; exit 1; }
python3 -c 'import pyte' || { echo "post-create: pyte is missing" >&2; exit 1; }

cat << 'EOF'

Ready.

    make check         gofmt, vet, and tests -- exactly what CI runs
    make screenshot    redraw docs/screenshot.png and docs/screenshot-warning.png

Redrawing is a deliberate act: see AGENTS.md for when it is called for.
EOF