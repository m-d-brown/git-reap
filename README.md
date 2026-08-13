# git-reap

Delete old branches, and the worktrees sitting on them.

Handles several types of branches that pile up: merged, squash-merged whose
remote branch is gone, ideas abandoned long ago, and — especially for
agent-based development — detached worktrees like those under
`.claude/worktrees`. `git reap` finds all four, shows you what it
found, and deletes only what you pick using `fzf`.

![git reap picking through the candidates in a repository](docs/screenshot.png)

- **Worktrees, not just branches.** Branch cleaners leave worktrees where they
  lie, and a worktree holding a branch is exactly what stops the branch from
  being deleted. `git reap` removes both, in that order.
- **The ones agents leave behind.** Detached worktrees drifting under
  `.claude/worktrees` — what Claude Code and similar tooling leave when a task
  ends — are found by age, not by path, so any layout works.
- **Four reasons, one pass.** Merged, upstream gone, unused, detached: the
  squash-merged pull request and the six-month-old spike are as findable as the
  cleanly merged branch.
- **You pick.** Candidates go through [fzf](https://github.com/junegunn/fzf)
  with each one's recent history in the preview pane. Nothing is deleted that
  you did not mark.
- **It says what it skipped.** `--dry-run` lists the candidates *and* the
  worktrees it left alone, with the reason for each.
- **Nothing to configure.** No config file, no state, no remote API, no token.
  One binary, and `git` and `fzf` on your `PATH`.

## What qualifies for deletion

| Reason          | What it means                                                                           |
| --------------- | --------------------------------------------------------------------------------------- |
| `merged`        | the branch is already contained in the base branch                                      |
| `upstream gone` | the remote branch was deleted — what a squash-merged, closed pull request leaves behind |
| `unused`        | no commits on the branch in the last `--days` days (90 by default)                      |
| `detached`      | a clean worktree on a detached HEAD, idle for `--days` days                             |

Worktrees are removed before branches, because git refuses to delete a branch
that a worktree has checked out.

**Never touched:** the base branch, the branch you are on, the branch the main
worktree holds, the main worktree itself, any worktree that is locked, has
uncommitted changes, or is the one you are standing in. And any branch that is
none of the four above.

## Install

With Go 1.22 or newer:

```sh
go install github.com/m-d-brown/git-reap@latest
```

Or from a clone:

```sh
git clone https://github.com/m-d-brown/git-reap
cd git-reap
go build -o ~/bin/git-reap .
```

Anything named `git-reap` on your `PATH` is reachable as `git reap`, so
that is all the installation there is. [fzf](https://github.com/junegunn/fzf) is
what makes the picker work; without it, `--dry-run` and `--all` still do.

## Usage

```
usage: git reap [options] [base]

Delete merged, gone, and unused branches and their worktrees.

  base            branch to measure 'merged' against (default: origin/HEAD,
                  falling back to origin/main, origin/master, main, master)

options:
  -n, --dry-run   list the candidates, and what was skipped, without deleting
  -a, --all       take every candidate instead of picking through fzf
  -y, --yes       skip the confirmation prompt that --all asks for
  -d, --days N    how quiet a branch or detached worktree must be to count as
                  unused (default: 90)
      --no-fetch  skip the 'git fetch --prune' that refreshes remote state
  -h, --help      show this message
```

Run it with no arguments and pick through the list: <kbd>TAB</kbd> marks a row,
<kbd>Enter</kbd> deletes what is marked, <kbd>Esc</kbd> walks away. The preview
pane shows the recent history of whatever the cursor is on.

```sh
git reap                # pick through the candidates
git reap -n             # just look, and see what was skipped and why
git reap -a -y          # take everything, no questions
git reap -d 30          # count a month of silence as unused
git reap develop        # measure "merged" against develop
```

`--dry-run` also explains its omissions, which is usually where the interesting
information is:

```
worktree  worktrees/csv-export            merged    9 days ago    clean        feature/csv-export
worktree  .claude/worktrees/agent-7f21e0  detached  6 months ago  clean        detached at 1baafd65
branch    chore/bump-deps                 merged    5 days ago    no upstream  chore: bump axios, vite, and typescript
kept    worktree /src/checkout-service/.claude/worktrees/agent-e5a018 (detached but recent)
```

## How it decides

Each run starts with `git fetch --prune`, which is what makes `[gone]` mean
anything: until the remote refs are pruned, a branch whose upstream was deleted
still looks alive. Pass `--no-fetch` when you are offline.

The base branch is `origin/HEAD` — the default branch your clone recorded. If
your clone never recorded one, `git remote set-head origin -a` fixes that;
failing that, `git reap` tries `origin/main`, `origin/master`, `main`, and
`master`, and you can always name a base yourself.

A branch reported as `merged` is deleted with `git branch -d`, so git gets the
last word. `upstream gone` and `unused` branches are not merged as far as git is
concerned, so those need `-D`. That is why a branch that is both merged and idle
is reported as merged: it is the gentler of the two.

## Compared to other tools

The `git branch --merged | xargs git branch -d` one-liner everyone keeps in
their dotfiles also deletes merged branches, and does it well. Two things are
different here.

Worktrees are first-class. Operating on branches alone leaves the worktree on
disk and, because git will not delete a branch a worktree has checked out,
sometimes leaves the branch too. And a branch does not have to be
merged to be finished: `git reap` also takes the squash-merged branch whose
upstream is gone, and the branch nobody has touched in three months, which is
what most abandoned work actually looks like.

## Development

```sh
go test ./...     # unit tests, plus an integration test against a real repository
go vet ./...
```

The integration test builds the binary and runs it against a temporary
repository holding one of everything — a merged branch, a squash-merged branch
whose upstream is gone, an idle branch, an active branch, and worktrees that are
merged, dirty, idle-detached, and freshly detached — with a bare repository next
door standing in for the remote, so the fetch is real but offline.

The screenshot above is not a mockup, and it is not hand-maintained:

```sh
scripts/screenshot.sh
```

builds the binary, builds a demo repository with something of each kind left
lying around in it (`scripts/demo-repo.sh`), runs the real picker against that
in a pty, and draws the screen it left behind (`scripts/capture.py`) into
`docs/screenshot.png`. Commit dates in the demo are relative to today, so the
ages hold still between runs; the hashes move, because they hash those dates.
Needs `fzf`, `rsvg-convert`, and `python3` with [pyte](https://pypi.org/project/pyte/).

## License

MIT
