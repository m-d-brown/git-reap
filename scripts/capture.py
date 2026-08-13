#!/usr/bin/env python3
"""Run a terminal program in a pty and save the screen it drew as an SVG.

    usage: capture.py <output.svg> <working-directory> <command> [args...]

The program gets a real pty, so it behaves exactly as it would in a terminal.
Its output is replayed through a terminal emulator (pyte), and the resulting
grid of cells -- same characters, same colours -- is what lands in the SVG.
Every run of same-styled cells is drawn at its own grid coordinates with an
explicit textLength, so the columns line up in whatever monospace font the
viewer happens to have.
"""

import fcntl
import os
import pty
import struct
import subprocess
import sys
import termios
import time
from xml.sax.saxutils import escape

import pyte

COLUMNS, ROWS = 140, 32
CELL_W, CELL_H = 8.0, 17.0
PADDING = 18.0
TITLEBAR = 34.0
SETTLE_SECONDS = 2.0

# A dark palette in the neighbourhood of what most terminals ship with.
PALETTE = {
    "black": "#2b3038",
    "red": "#e06c75",
    "green": "#98c379",
    "brown": "#d19a66",
    "yellow": "#e5c07b",
    "blue": "#61afef",
    "magenta": "#c678dd",
    "cyan": "#56b6c2",
    "white": "#c8ccd4",
    "brightblack": "#5c6370",
    "brightred": "#e06c75",
    "brightgreen": "#98c379",
    "brightyellow": "#e5c07b",
    "brightblue": "#61afef",
    "brightmagenta": "#c678dd",
    "brightcyan": "#56b6c2",
    "brightwhite": "#ffffff",
}
BACKGROUND = "#21252b"
FOREGROUND = "#abb2bf"
TITLEBAR_FILL = "#2b3038"
FONTS = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"


def colour(name, default):
    """Turn one of pyte's colour names into something SVG understands."""
    if name == "default":
        return default
    # Anything pyte does not have a name for arrives as a bare hex triplet.
    return PALETTE.get(name, "#" + name)


def capture(command, cwd):
    """Run command in a pty until it settles, and return everything it printed."""
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLUMNS, 0, 0))

    def take_the_pty():
        # A full-screen program reads its keystrokes from /dev/tty, so the pty
        # has to be the child's controlling terminal, not just its stdin.
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    process = subprocess.Popen(
        command,
        cwd=cwd,
        env=dict(os.environ, TERM="xterm-256color", COLUMNS=str(COLUMNS), LINES=str(ROWS)),
        stdin=slave,
        stdout=slave,
        stderr=slave,
        preexec_fn=take_the_pty,
    )
    os.close(slave)
    os.set_blocking(master, False)

    output = bytearray()
    deadline = time.time() + SETTLE_SECONDS
    while time.time() < deadline:
        try:
            chunk = os.read(master, 65536)
        except (BlockingIOError, OSError):
            chunk = b""
        if chunk:
            output.extend(chunk)
        else:
            time.sleep(0.05)

    process.terminate()
    process.wait()
    os.close(master)
    return bytes(output)


def runs(line):
    """Split one screen row into (start, end, style, text) runs of one style."""
    start, style, text = 0, None, ""
    for column in range(COLUMNS):
        cell = line[column]
        here = (cell.fg, cell.bg, cell.bold, cell.reverse)
        if here != style:
            if style is not None:
                yield start, column, style, text
            start, style, text = column, here, ""
        text += cell.data or " "
    yield start, COLUMNS, style, text


def draw(start, end, style, text, y):
    """Draw one run: its background if it has one, then its characters."""
    foreground, background, bold, reverse = style
    fill, behind = colour(foreground, FOREGROUND), colour(background, BACKGROUND)
    if reverse:
        fill, behind = behind, fill

    x, span = PADDING + start * CELL_W, (end - start) * CELL_W
    parts = []
    if behind != BACKGROUND:
        parts.append(
            f'<rect x="{x:.1f}" y="{y - CELL_H * 0.75:.1f}" width="{span:.1f}" '
            f'height="{CELL_H:.1f}" fill="{behind}"/>'
        )
    if text.strip():
        weight = ' font-weight="bold"' if bold else ""
        parts.append(
            f'<text x="{x:.1f}" y="{y:.1f}" fill="{fill}"{weight} textLength="{span:.1f}" '
            f'lengthAdjust="spacing" xml:space="preserve">{escape(text)}</text>'
        )
    return parts


def render(data, title):
    """Replay the captured output and draw the screen it left behind."""
    screen = pyte.Screen(COLUMNS, ROWS)
    pyte.Stream(screen).feed(data.decode("utf-8", "replace"))

    width = COLUMNS * CELL_W + 2 * PADDING
    height = ROWS * CELL_H + 2 * PADDING + TITLEBAR
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width:.0f}" '
        f'height="{height:.0f}" viewBox="0 0 {width:.0f} {height:.0f}" '
        f'font-family="{FONTS}" font-size="13">',
        f'<rect width="{width:.0f}" height="{height:.0f}" rx="10" fill="{BACKGROUND}"/>',
        f'<rect width="{width:.0f}" height="{TITLEBAR:.0f}" rx="10" fill="{TITLEBAR_FILL}"/>',
        f'<rect y="{TITLEBAR - 10:.0f}" width="{width:.0f}" height="10" fill="{TITLEBAR_FILL}"/>',
        f'<text x="{width / 2:.0f}" y="{TITLEBAR / 2 + 4:.0f}" fill="#8b93a1" '
        f'font-size="12" text-anchor="middle">{escape(title)}</text>',
    ]
    for i, dot in enumerate(("#ff5f57", "#febc2e", "#28c840")):
        parts.append(f'<circle cx="{PADDING + i * 20:.0f}" cy="{TITLEBAR / 2:.0f}" r="6" fill="{dot}"/>')

    for row in range(ROWS):
        y = TITLEBAR + PADDING + row * CELL_H + CELL_H * 0.75
        for start, end, style, text in runs(screen.buffer[row]):
            parts.extend(draw(start, end, style, text, y))

    parts.append("</svg>")
    return "\n".join(parts)


def main(argv):
    destination, cwd, *command = argv
    with open(destination, "w") as handle:
        handle.write(render(capture(command, cwd), title="git reap"))
    print(f"wrote {destination}")


if __name__ == "__main__":
    main(sys.argv[1:])
