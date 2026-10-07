# The Debian base from the Dev Containers project: git, curl, zsh, and a
# non-root "vscode" user. The Go version is not pinned here -- post-create.sh
# takes it from go.mod, the file that already owns it.
FROM mcr.microsoft.com/devcontainers/base:bookworm

# What scripts/screenshot.sh shells out to, and cannot install for itself:
#
#   librsvg2-bin   rsvg-convert, which turns capture.py's SVG into the PNG
#   fonts-dejavu-core  a monospace font for rsvg-convert to draw with. The SVG
#                  names a list of families and DejaVu Sans Mono is the entry a
#                  Debian box can satisfy; with none of them installed the text
#                  falls back to a proportional face and the columns wander.
#
# Both come from apt because neither is in mise's registry, and neither needs
# pinning more finely than this: one rasterises an SVG, the other is a font held
# still by its own package. Nothing either of them does shows up as a glyph.
#
# fzf is deliberately not here. It is the one tool whose version is visible in
# the picture -- the cursor and the marker are drawn differently from release to
# release -- so mise.toml pins it, where a bump is a one-line diff rather than a
# base-image change. Installing the distro's fzf alongside would only give a
# misconfigured run something stale to fall back to, quietly.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        librsvg2-bin \
        fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*

USER vscode

# mise's shims put the toolchain on PATH for every process, not just interactive
# shells, and ~/.local/bin is where mise and uv themselves land. The venv comes
# first because screenshot.sh calls python3 by name and needs the one that has
# pyte in it; post-create.sh is what creates it.
ENV PATH=/home/vscode/.venv/bin:/home/vscode/.local/share/mise/shims:/home/vscode/.local/bin:$PATH

# Shells need the toolchain too, and neither kind inherits it from the ENV above:
# an interactive shell wants mise activated, and a login shell has already had
# /etc/profile rebuild PATH from scratch -- and does not read ~/.bashrc, so
# activating there does not reach it. Restating PATH in ~/.profile is what keeps
# `bash -lc "go build ."` working.
RUN curl -fsSL https://mise.run | sh \
    && echo 'eval "$(mise activate bash)"' >> ~/.bashrc \
    && echo 'eval "$(mise activate zsh)"' >> ~/.zshrc \
    && echo 'export PATH="$HOME/.venv/bin:$HOME/.local/share/mise/shims:$HOME/.local/bin:$PATH"' >> ~/.profile

# uv brings its own Python, so capture.py does not depend on whatever the base
# image happens to ship.
RUN curl -LsSf https://astral.sh/uv/install.sh | sh

# Claude Code, installed the same way: a single binary in ~/.local/bin. The
# ~/.claude directory is created here rather than by the volume mounted over it,
# so the volume inherits this ownership instead of being created by root.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && mkdir -p ~/.claude
