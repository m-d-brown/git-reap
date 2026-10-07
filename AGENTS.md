# Working in this repository

`git reap` deletes branches that are finished with, and the worktrees sitting on
them. Go, standard library only, with `fzf` shelled out to for the picker.

## The gate

```sh
make check
```

gofmt, `go vet`, and the tests. CI runs that target rather than its own copy of
the steps, so passing it here is passing it there. Run it before calling
anything done.

## docs/*.png are generated

`docs/screenshot.png` and `docs/screenshot-warning.png` are photographs of the
real picker running against a real repository. Never edit them by hand, and
never describe them as mockups. Redraw with:

```sh
make screenshot
```

**Redraw in the same commit as any change to what the picker puts on screen** —
a column, a reason string, a preview line, the row order, or
`scripts/demo-repo.sh`. A change that cannot move a pixel — tests, comments, the
README — needs no redraw, and nothing in the repository will stop you shipping a
stale picture, so this is on you.

Redrawing when unsure is free. The demo repository dates its commits against the
current UTC day rather than the current second, so a redraw with nothing behind
it produces identical bytes and leaves `git status` empty. If `docs/` comes back
modified, the picture genuinely changed.

Redraw **in the devcontainer**. It pins `fzf` to the version Debian stable
ships; a newer `fzf` draws its cursor and marker with different glyphs, which
lands in the diff as a visual change having nothing to do with your work.

## The README quotes real output

Three fenced blocks in `README.md` are pasted from actual runs, and they drift
silently when behaviour changes. After touching flags, columns, reason strings,
or `--debug`, check them against a real run:

```sh
make demo
cd /tmp/git-reap-demo/checkout-service
<repo>/git-reap --help
<repo>/git-reap --no-fetch --dry-run --all
<repo>/git-reap --no-fetch --debug
```

- the `usage:` block matches `--help` exactly;
- the `--dry-run` block matches `--dry-run --all`, including the trailing
  `N rows are "only here"` count, which is easy to leave behind;
- the `--debug` block is an abridgement, keeping a few rows of each table. The
  commit hashes in it are deliberately stale — they rehash every day — but the
  ages and the outcomes are not.

## The demo repository is load-bearing

Every branch and worktree in `scripts/demo-repo.sh` exists to exercise one rule,
and several exist in order to be *passed over*: `feature/billing-portal` is too
recent to qualify, `worktrees/invoice-pdf` is dirty and pins its branch,
`.claude/worktrees/agent-e5a018` was used too recently. `spike/graphql-gateway`
is the entire subject of the warning screenshot. Dropping or re-aging one
quietly deletes a case from the screenshots and the README both.

Its clock is tied to today on purpose. `unused` means "no commit in the last 90
days", measured against the real clock when `git reap` runs — so freezing the
demo at a fixed date changes which rows are offered at all, and a date far
enough ahead offers no idle branches and no detached worktrees whatsoever.

## Style

Comments here carry their reasoning, often at length, and are expected to: the
interesting part of a decision is why it went that way and what it would break
to undo. Match the density of the file you are in rather than trimming to the
usual sparseness.
