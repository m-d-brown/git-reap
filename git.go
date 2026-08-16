package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// gatherStates looks at each worktree on disk: does it exist, is it dirty, how
// old is its last commit, and is its HEAD already in base.
func gatherStates(worktrees []Worktree, base string) map[string]State {
	states := make(map[string]State, len(worktrees))
	for _, worktree := range worktrees {
		if info, err := os.Stat(worktree.Path); err != nil || !info.IsDir() {
			states[worktree.Path] = State{Relative: "gone"}
			continue
		}
		stamp := gitTry("-C", worktree.Path, "log", "-1", "--format=%ct%x1f%cr")
		unix, relative, _ := strings.Cut(stamp, field)
		committedAt, _ := strconv.ParseInt(unix, 10, 64)
		if relative == "" {
			relative = "unknown"
		}
		states[worktree.Path] = State{
			Exists:      true,
			Dirty:       gitTry("-C", worktree.Path, "status", "--porcelain") != "",
			CommittedAt: committedAt,
			Relative:    relative,
			InBase:      gitSucceeds("merge-base", "--is-ancestor", worktree.Head, base),
		}
	}
	return states
}

// worktreeHolding returns the path of the worktree that has branch checked out.
func worktreeHolding(branch string) string {
	for _, worktree := range parseWorktrees(gitTry("worktree", "list", "--porcelain")) {
		if worktree.Branch == branch {
			return worktree.Path
		}
	}
	return ""
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
