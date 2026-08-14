#!/bin/bash
# Build a throwaway repository for the README screenshot: a project someone has
# been working in long enough to have left things behind.
#
#   usage: scripts/demo-repo.sh <directory>
#
# Commit dates are set relative to today, so the ages in the screenshot ("3
# weeks ago", "6 months ago") stay put no matter when it is regenerated. The
# commit hashes do move, since they hash those dates.
set -euo pipefail

ROOT=${1:?usage: demo-repo.sh <directory>}
rm -rf "$ROOT"
mkdir -p "$ROOT"

# Nothing of the real git config -- signing, hooks, aliases -- leaks in, and the
# commits get an identity that suits a demo.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="Ada Lovelace" GIT_AUTHOR_EMAIL=ada@example.com
export GIT_COMMITTER_NAME="Ada Lovelace" GIT_COMMITTER_EMAIL=ada@example.com

REMOTE=$ROOT/origin.git
WORK=$ROOT/checkout-service
NOW=$(date +%s)

git init -q --bare -b main "$REMOTE"
git clone -q "$REMOTE" "$WORK"
cd "$WORK"

# commit <subject> <days-ago>. "@<epoch> +0000" is the one date format git takes
# everywhere, and unlike `date -d` it works the same on macOS and Linux.
commit() {
  local file when
  file=$(printf '%s' "$1" | tr -c 'a-zA-Z0-9' '_').txt
  when="@$((NOW - $2 * 86400)) +0000"
  printf '%s\n' "$1" > "$file"
  git add "$file"
  GIT_AUTHOR_DATE="$when" GIT_COMMITTER_DATE="$when" git commit -qm "$1"
}

# land <branch> <subject> <days-ago>: merged into main, the way a pull request
# that was not squashed lands.
land() {
  git checkout -q -b "$1"
  commit "$2" "$3"
  git checkout -q main
  git merge -q "$1"
}

# land_unpushed <branch> <subject> <days-ago>: pushed, carried on locally, then
# merged into main. Contained in origin/main, so deleting it loses nothing, but
# ahead of its own remote branch, which is the ref `git branch -d` measures
# against -- the case that needs -D despite being perfectly safe.
land_unpushed() {
  git checkout -q -b "$1"
  commit "$2" "$(($3 + 2))"
  git push -q -u origin "$1"
  commit "$2, second pass" "$3"
  git checkout -q main
  git merge -q "$1"
}

# squash <branch> <subject> <days-ago>: pushed, then deleted on the remote,
# which is what a squash-merged and closed pull request leaves behind -- the
# local branch tracks an upstream that is gone, and is not merged.
squash() {
  git checkout -q -b "$1"
  commit "$2" "$3"
  git push -q -u origin "$1"
  git checkout -q main
  git push -q origin --delete "$1"
}

# abandon <branch> <subject> <days-ago>: started, then left alone.
abandon() {
  git checkout -q -b "$1"
  commit "$2" "$3"
  git checkout -q main
}

commit "initial commit" 400
git push -q -u origin main
# A fresh clone of an empty repository has no origin/HEAD; set it the way a
# clone of a repository with commits in it would have.
git remote set-head origin -a

land feature/avatar-upload "feat(profile): upload and crop avatars" 12
land chore/bump-deps "chore: bump axios, vite, and typescript" 5
land feature/csv-export "feat(reports): export a run as CSV" 9

land_unpushed fix/session-timeout "fix(auth): stop refreshing an expired session" 7

squash fix/login-redirect "fix(auth): keep the redirect target across SSO" 21
squash feature/rate-limits "feat(api): per-token rate limits" 34

abandon spike/graphql-gateway "spike: sketch a graphql gateway in front of the REST api" 190
abandon wip/flaky-scheduler-test "wip: try to reproduce the flaky scheduler test" 145
# Recent and unmerged, so never a candidate.
abandon feature/billing-portal "feat(billing): stub out the customer portal" 1

git push -q origin main

# A worktree per branch someone is living in, plus the detached ones agent
# tooling leaves under .claude/worktrees.
git worktree add -q worktrees/csv-export feature/csv-export
git worktree add -q --detach .claude/worktrees/agent-7f21e0 spike/graphql-gateway
git worktree add -q --detach .claude/worktrees/agent-b3c94d wip/flaky-scheduler-test
git worktree add -q --detach .claude/worktrees/agent-e5a018
