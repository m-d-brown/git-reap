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

// findBase works out which branch to measure "merged" against.
//
// origin/HEAD records the remote's default branch, when the clone bothered to
// set it -- `git remote set-head origin -a` refreshes it. Repositories with no
// remote fall through to the local names.
func findBase(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if head := gitTry("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); head != "" {
		return head
	}
	for _, ref := range baseFallbacks {
		if gitTry("rev-parse", "--verify", "--quiet", ref) != "" {
			return ref
		}
	}
	return ""
}

// gatherStates looks at each worktree on disk: does it exist, is it dirty, how
// old is its last commit.
func gatherStates(worktrees []Worktree) map[string]State {
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
func execute(items []Item, selected map[string]bool, worktrees []Worktree) {
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
		// A squash-merged or simply idle branch is not merged as far as git is
		// concerned, so -d would refuse it. -d elsewhere keeps git's check.
		flag := "-D"
		if item.Reason == Merged {
			flag = "-d"
		}
		gitRun("branch", flag, item.Key)
	}
}
