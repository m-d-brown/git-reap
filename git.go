package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// baseFallbacks are the bases to try, in order, when origin/HEAD is not
// recorded in the clone.
var baseFallbacks = []string{"origin/main", "origin/master", "main", "master"}

// gitCapture runs git with args and returns its stdout, stripped of whitespace.
func gitCapture(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	command := exec.Command("git", args...)
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gitTry is gitCapture for the commands we are happy to see fail: a missing ref
// or a broken worktree comes back as "".
func gitTry(args ...string) string {
	output, err := gitCapture(args...)
	if err != nil {
		return ""
	}
	return output
}

// gitRun runs git with args, letting it write to our stdout and stderr.
func gitRun(args ...string) bool {
	command := exec.Command("git", args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run() == nil
}

// gitSucceeds runs git with args for its exit status alone, which is how the
// plumbing commands that answer yes-or-no questions report themselves.
func gitSucceeds(args ...string) bool {
	_, err := gitCapture(args...)
	return err == nil
}

// findBase works out which branch to measure "merged" against, and says where
// that came from.
//
// origin/HEAD records the remote's default branch, when the clone bothered to
// set it -- `git remote set-head origin -a` refreshes it. Repositories with no
// remote fall through to the local names.
//
// The second result exists for --debug: a base that quietly resolved to
// something other than what you assumed is the usual reason a run offers
// nothing, and it is invisible until something says which ref was picked and
// which rule picked it.
func findBase(explicit string) (string, string) {
	if explicit != "" {
		return explicit, "given on the command line"
	}
	if head := gitTry("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); head != "" {
		return head, "origin/HEAD, the default branch this clone recorded"
	}
	for _, ref := range baseFallbacks {
		if gitTry("rev-parse", "--verify", "--quiet", ref) != "" {
			return ref, "a fallback: origin/HEAD is not set, and this is the first of " +
				strings.Join(baseFallbacks, ", ") + " that exists"
		}
	}
	return "", "nothing matched: no origin/HEAD, and none of " + strings.Join(baseFallbacks, ", ") + " exists"
}

// treeOf is the tree a commit-ish points at, or "" when it does not resolve.
func treeOf(rev string) string {
	return gitTry("rev-parse", "--verify", "--quiet", rev+"^{tree}")
}

// landedIn reports whether rev's changes are already in base under other
// commits -- squash-merged, rebased, or cherry-picked -- which is the case
// `git branch --merged` cannot see: it asks whether the commits are in base,
// and after a squash they never will be.
//
// The question asked instead is whether merging rev into base would change
// anything. merge-tree does that merge in memory, touching no worktree and no
// index, and a merge whose tree is base's own tree is one that brings nothing.
// Being a three-way merge rather than a comparison of files, it is not fooled
// by base having moved on elsewhere in the same files since; base having
// rewritten the very lines the branch changed is a conflict, and reads as not
// landed. So does any failure at all, including a git older than 2.38, which
// has no --write-tree: a branch this cannot vouch for keeps its "only here".
//
// The objects the merge writes are unreachable, and gc collects them. When the
// branch has landed there are none, since the merged tree is base's own.
func landedIn(rev, base, baseTree string) bool {
	if baseTree == "" {
		return false
	}
	output, err := gitCapture("merge-tree", "--write-tree", base, rev)
	tree, _, _ := strings.Cut(output, "\n")
	return err == nil && tree == baseTree
}

// landedBranches picks out the branches that are not merged into base but whose
// changes are already in it. Only those: a merged branch is in base by its
// commits, which says the same thing more cheaply.
func landedBranches(branches []Branch, merged map[string]bool, base, baseTree string) map[string]bool {
	landed := map[string]bool{}
	for _, branch := range branches {
		// The full ref, so that a tag sharing the branch's name cannot stand in
		// for it.
		if !merged[branch.Name] && landedIn("refs/heads/"+branch.Name, base, baseTree) {
			landed[branch.Name] = true
		}
	}
	return landed
}

// gatherStates looks at each worktree on disk: does it exist, how much is
// uncommitted, how old is its last commit, when was it last used, and is its
// HEAD already in base, by its commits or by its changes.
func gatherStates(worktrees []Worktree, base, baseTree string) map[string]State {
	states := make(map[string]State, len(worktrees))
	for _, worktree := range worktrees {
		if info, err := os.Stat(worktree.Path); err != nil || !info.IsDir() {
			states[worktree.Path] = State{Relative: "gone", TouchedRelative: "gone"}
			continue
		}
		stamp := gitTry("-C", worktree.Path, "log", "-1", "--format=%ct%x1f%cr")
		unix, relative, _ := strings.Cut(stamp, field)
		committedAt, _ := strconv.ParseInt(unix, 10, 64)
		if relative == "" {
			relative = "unknown"
		}
		dirty := len(lines(gitTry("-C", worktree.Path, "status", "--porcelain")))
		touchedAt := lastUsed(worktree.Path, committedAt)
		inBase := gitSucceeds("merge-base", "--is-ancestor", worktree.Head, base)
		states[worktree.Path] = State{
			Exists:          true,
			DirtyCount:      dirty,
			Dirty:           dirty > 0,
			CommittedAt:     committedAt,
			Relative:        relative,
			TouchedAt:       touchedAt,
			TouchedRelative: humanize(touchedAt),
			InBase:          inBase,
			Landed:          !inBase && landedIn(worktree.Head, base, baseTree),
		}
	}
	return states
}

// lastUsed is when this worktree was last checked out or committed on, which is
// the honest measure of whether it has been abandoned. It falls back to the
// commit date when the files it reads are unavailable.
//
// The signal is the mtime of HEAD in the worktree's own admin directory, and
// the choice of file matters. The obvious candidate, the index, is no good:
// `git status` rewrites it to refresh cached stat information, so gatherStates
// above would touch every worktree it looked at and then find them all fresh.
// Nothing but a checkout writes HEAD -- and on a detached worktree HEAD holds
// the raw sha rather than a ref, so committing rewrites it too, which is
// exactly the activity worth noticing on the worktrees this rule is for.
func lastUsed(path string, committedAt int64) int64 {
	if admin := gitTry("-C", path, "rev-parse", "--absolute-git-dir"); admin != "" {
		if info, err := os.Stat(filepath.Join(admin, "HEAD")); err == nil {
			return info.ModTime().Unix()
		}
	}
	// A worktree whose admin directory we cannot read still has a directory of
	// its own, whose mtime moves when files are added or removed at the top.
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Unix()
	}
	return committedAt
}

// holderOf returns the path of the worktree that has branch checked out.
func holderOf(branch string, worktrees []Worktree) string {
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			return worktree.Path
		}
	}
	return ""
}

// worktreeHolding is holderOf for the caller with no listing to hand: the
// --preview process, which is a fresh run given nothing but a token.
func worktreeHolding(branch string) string {
	return holderOf(branch, parseWorktrees(gitTry("worktree", "list", "--porcelain")))
}

// execute removes the selected worktrees, then deletes the selected branches.
// It returns how many of them git refused, so that a run which did not do what
// it was asked does not look like one that did.
func execute(items []Item, selected map[string]bool, worktrees []Worktree) int {
	failed := 0
	holder := map[string]string{}
	for _, worktree := range worktrees {
		if worktree.Branch != "" {
			holder[worktree.Branch] = worktree.Path
		}
	}

	removed := map[string]bool{}
	for _, item := range items {
		if item.Kind != WorktreeKind || !selected[item.Token()] {
			continue
		}
		if gitRun("worktree", "remove", item.Key) {
			removed[item.Key] = true
			fmt.Println("removed worktree " + item.Key)
		} else {
			fmt.Fprintf(os.Stderr, "git-reap: could not remove worktree %s\n", item.Key)
			failed++
		}
	}

	for _, item := range items {
		if item.Kind != BranchKind || !selected[item.Token()] {
			continue
		}
		if heldBy, held := holder[item.Key]; held && !removed[heldBy] {
			fmt.Printf("kept branch %s (checked out at %s)\n", item.Key, heldBy)
			continue
		}
		// Item.Force already worked out whether git's own -d check would refuse
		// this branch; it asks about the upstream, not about the base we
		// measured "merged" against.
		flag := "-d"
		if item.Force {
			flag = "-D"
		}
		if !gitRun("branch", flag, item.Key) {
			fmt.Fprintf(os.Stderr, "git-reap: could not delete branch %s\n", item.Key)
			failed++
		}
	}
	return failed
}
