package main

import (
	"strconv"
	"strings"
	"testing"
)

// branchLine builds one line of `git for-each-ref --format=branchFormat` output.
func branchLine(name string, committedAt int64, relative, subject, upstream, track string) string {
	return strings.Join([]string{
		name, strconv.FormatInt(committedAt, 10), relative, subject, upstream, track,
	}, field)
}

// worktreePorcelain joins blocks the way `git worktree list --porcelain` does.
func worktreePorcelain(blocks ...string) string {
	return strings.Join(blocks, "\n\n") + "\n"
}

func TestParseBranches(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		line := branchLine("wip", 1700000000, "3 days ago", "fix the thing", "origin/wip", "[ahead 2]")
		want := Branch{"wip", 1700000000, "3 days ago", "fix the thing", "origin/wip", "[ahead 2]"}
		if got := parseBranches(line); len(got) != 1 || got[0] != want {
			t.Errorf("parseBranches(%q) = %+v, want [%+v]", line, got, want)
		}
	})

	t.Run("branch without upstream has neither upstream nor track", func(t *testing.T) {
		got := parseBranches(branchLine("solo", 100, "1 day ago", "s", "", ""))
		if got[0].Upstream != "" || got[0].Track != "" {
			t.Errorf("Upstream = %q, Track = %q, want both empty", got[0].Upstream, got[0].Track)
		}
	})

	t.Run("in sync keeps its upstream and empty track", func(t *testing.T) {
		// git reports an empty track both for this and for a branch with no
		// upstream at all, so Upstream is the only thing that tells them apart.
		got := parseBranches(branchLine("level", 100, "1 day ago", "s", "origin/level", ""))
		if got[0].Upstream != "origin/level" || got[0].Track != "" {
			t.Errorf("Upstream = %q, Track = %q", got[0].Upstream, got[0].Track)
		}
	})

	t.Run("upstream columns missing entirely", func(t *testing.T) {
		// gitCapture strips its output, so a listing ending in a branch with no
		// upstream loses the trailing separators.
		line := strings.Join([]string{"last", "100", "1 day ago", "subject"}, field)
		got := parseBranches(line)
		if got[0].Upstream != "" || got[0].Track != "" {
			t.Errorf("Upstream = %q, Track = %q, want both empty", got[0].Upstream, got[0].Track)
		}
	})

	t.Run("subject containing spaces and punctuation", func(t *testing.T) {
		line := branchLine("b", 100, "1 day ago", "feat: add a, b; and c", "", "")
		if got := parseBranches(line)[0].Subject; got != "feat: add a, b; and c" {
			t.Errorf("Subject = %q", got)
		}
	})

	t.Run("multiple branches", func(t *testing.T) {
		output := branchLine("a", 100, "now", "s", "origin/a", "[gone]") + "\n" +
			branchLine("b", 100, "now", "s", "", "")
		got := parseBranches(output)
		if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
			t.Errorf("parseBranches = %+v", got)
		}
	})

	t.Run("no branches", func(t *testing.T) {
		if got := parseBranches(""); len(got) != 0 {
			t.Errorf("parseBranches(\"\") = %+v, want empty", got)
		}
	})
}

func TestParseWorktrees(t *testing.T) {
	t.Run("branch ref is shortened and head kept", func(t *testing.T) {
		porcelain := worktreePorcelain("worktree /repo\nHEAD abc123\nbranch refs/heads/main")
		want := Worktree{"/repo", "abc123", "main", false}
		if got := parseWorktrees(porcelain); len(got) != 1 || got[0] != want {
			t.Errorf("parseWorktrees = %+v, want [%+v]", got, want)
		}
	})

	t.Run("detached worktree has no branch", func(t *testing.T) {
		porcelain := worktreePorcelain(
			"worktree /repo\nHEAD abc123\nbranch refs/heads/main",
			"worktree /wt\nHEAD def456\ndetached",
		)
		got := parseWorktrees(porcelain)[1]
		if got.Branch != "" || got.Head != "def456" {
			t.Errorf("parseWorktrees[1] = %+v", got)
		}
	})

	t.Run("locked with and without a reason", func(t *testing.T) {
		porcelain := worktreePorcelain(
			"worktree /repo\nHEAD a\nbranch refs/heads/main",
			"worktree /one\nHEAD a\nbranch refs/heads/one\nlocked",
			"worktree /two\nHEAD a\nbranch refs/heads/two\nlocked on a usb drive",
		)
		want := []bool{false, true, true}
		for i, worktree := range parseWorktrees(porcelain) {
			if worktree.Locked != want[i] {
				t.Errorf("worktree %d Locked = %v, want %v", i, worktree.Locked, want[i])
			}
		}
	})

	t.Run("flags do not leak between blocks", func(t *testing.T) {
		porcelain := worktreePorcelain(
			"worktree /repo\nHEAD a\nbranch refs/heads/main\nlocked",
			"worktree /wt\nHEAD b\nbranch refs/heads/feature",
		)
		want := Worktree{"/wt", "b", "feature", false}
		if got := parseWorktrees(porcelain)[1]; got != want {
			t.Errorf("parseWorktrees[1] = %+v, want %+v", got, want)
		}
	})

	t.Run("no worktrees", func(t *testing.T) {
		if got := parseWorktrees(""); len(got) != 0 {
			t.Errorf("parseWorktrees(\"\") = %+v, want empty", got)
		}
	})
}
