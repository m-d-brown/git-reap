package main

import (
	"reflect"
	"testing"
)

func set(names ...string) map[string]bool {
	members := map[string]bool{}
	for _, name := range names {
		members[name] = true
	}
	return members
}

func TestClassifyBranches(t *testing.T) {
	branch := func(name string, committedAt int64, track string) Branch {
		return Branch{name, committedAt, "1 day ago", "s", track}
	}
	const staleBefore = 50

	tests := []struct {
		name     string
		branches []Branch
		merged   map[string]bool
		want     map[string]Reason
	}{
		{
			name:     "merged branch qualifies",
			branches: []Branch{branch("done", 100, "")},
			merged:   set("done"),
			want:     map[string]Reason{"done": Merged},
		},
		{
			name:     "gone upstream qualifies without being merged",
			branches: []Branch{branch("squashed", 100, "[gone]")},
			want:     map[string]Reason{"squashed": Gone},
		},
		{
			name:     "idle branch qualifies as unused",
			branches: []Branch{branch("forgotten", 10, "")},
			want:     map[string]Reason{"forgotten": Unused},
		},
		{
			name:     "recent unmerged branch is left alone",
			branches: []Branch{branch("active", 100, "")},
			want:     map[string]Reason{},
		},
		{
			// The reason picks the delete flag, and a gone branch needs -D.
			name:     "gone wins over merged and unused",
			branches: []Branch{branch("squashed", 10, "[gone]")},
			merged:   set("squashed"),
			want:     map[string]Reason{"squashed": Gone},
		},
		{
			// Merged is the gentler reason: it deletes with -d rather than -D.
			name:     "merged wins over unused",
			branches: []Branch{branch("old", 10, "")},
			merged:   set("old"),
			want:     map[string]Reason{"old": Merged},
		},
		{
			name:     "branch exactly at the threshold is not unused yet",
			branches: []Branch{branch("edge", staleBefore, "")},
			want:     map[string]Reason{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyBranches(test.branches, test.merged, nil, staleBefore)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("classifyBranches = %v, want %v", got, test.want)
			}
		})
	}

	t.Run("protected branches are left alone", func(t *testing.T) {
		got := classifyBranches([]Branch{branch("main", 10, "[gone]")}, nil, set("main"), staleBefore)
		if len(got) != 0 {
			t.Errorf("classifyBranches = %v, want empty", got)
		}
	})
}

func TestPlanWorktrees(t *testing.T) {
	mainWorktree := Worktree{"/repo", "aaa", "main", false}
	state := func(exists, dirty bool, committedAt int64) State {
		return State{exists, dirty, committedAt, "1 day ago"}
	}
	const staleBefore = 50

	plan := func(worktree Worktree, worktreeState State, candidates map[string]Reason, current string) ([]Item, []Kept) {
		worktrees := []Worktree{mainWorktree}
		states := map[string]State{"/repo": state(true, false, 100)}
		if worktree.Path != "" {
			worktrees = append(worktrees, worktree)
			states[worktree.Path] = worktreeState
		}
		return planWorktrees(worktrees, states, candidates, current, staleBefore)
	}

	t.Run("the main worktree is never offered", func(t *testing.T) {
		items, kept := plan(Worktree{}, State{}, map[string]Reason{"main": Merged}, "/repo")
		if len(items) != 0 || len(kept) != 0 {
			t.Errorf("planWorktrees = %+v, %+v, want nothing", items, kept)
		}
	})

	t.Run("worktree on a qualifying branch is offered", func(t *testing.T) {
		items, kept := plan(
			Worktree{"/wt", "bbb", "done", false},
			state(true, false, 100),
			map[string]Reason{"done": Merged},
			"/repo",
		)
		want := Item{WorktreeKind, "/wt", Merged, "1 day ago", "clean", "done"}
		if len(items) != 1 || items[0] != want {
			t.Fatalf("items = %+v, want [%+v]", items, want)
		}
		if len(kept) != 0 {
			t.Errorf("kept = %+v, want nothing", kept)
		}
	})

	t.Run("worktree on an unused branch is offered", func(t *testing.T) {
		items, _ := plan(
			Worktree{"/wt", "bbb", "forgotten", false},
			state(true, false, 100),
			map[string]Reason{"forgotten": Unused},
			"/repo",
		)
		if items[0].Reason != Unused {
			t.Errorf("Reason = %q, want %q", items[0].Reason, Unused)
		}
	})

	t.Run("idle detached worktree is offered", func(t *testing.T) {
		items, _ := plan(Worktree{"/wt", "0123456789abcdef", "", false}, state(true, false, 10), nil, "/repo")
		if len(items) != 1 || items[0].Reason != Detached || items[0].Detail != "detached at 01234567" {
			t.Errorf("items = %+v", items)
		}
	})

	kepts := []struct {
		name       string
		worktree   Worktree
		state      State
		candidates map[string]Reason
		current    string
		want       Kept
	}{
		{
			name:     "recent detached worktree is kept",
			worktree: Worktree{"/wt", "abc", "", false},
			state:    state(true, false, 100),
			current:  "/repo",
			want:     Kept{"/wt", "detached but recent"},
		},
		{
			name:       "dirty worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(true, true, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{"/wt", "uncommitted changes"},
		},
		{
			name:       "locked worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", true},
			state:      state(true, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{"/wt", "locked"},
		},
		{
			name:       "current worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(true, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/wt",
			want:       Kept{"/wt", "current"},
		},
		{
			name:       "missing directory is left to prune",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(false, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{"/wt", "directory is gone; prune drops it"},
		},
		{
			// prune skips locked worktrees, so a locked entry survives even
			// when its directory is missing.
			name:     "locked is checked before the directory",
			worktree: Worktree{"/wt", "b", "done", true},
			state:    state(false, false, 100),
			current:  "/repo",
			want:     Kept{"/wt", "locked"},
		},
		{
			name:     "worktree on a branch still in use is kept",
			worktree: Worktree{"/wt", "b", "active", false},
			state:    state(true, false, 100),
			current:  "/repo",
			want:     Kept{"/wt", "active still in use"},
		},
	}

	for _, test := range kepts {
		t.Run(test.name, func(t *testing.T) {
			items, kept := plan(test.worktree, test.state, test.candidates, test.current)
			if len(items) != 0 {
				t.Errorf("items = %+v, want nothing", items)
			}
			if len(kept) != 1 || kept[0] != test.want {
				t.Errorf("kept = %+v, want [%+v]", kept, test.want)
			}
		})
	}
}

func TestBranchItems(t *testing.T) {
	t.Run("items are in name order with metadata", func(t *testing.T) {
		branches := []Branch{
			{"zeta", 100, "2 days ago", "later work", "[gone]"},
			{"alpha", 100, "3 weeks ago", "early work", ""},
		}
		items := branchItems(branches, map[string]Reason{"zeta": Gone, "alpha": Merged})
		want := []Item{
			{BranchKind, "alpha", Merged, "3 weeks ago", "no upstream", "early work"},
			{BranchKind, "zeta", Gone, "2 days ago", "[gone]", "later work"},
		}
		if !reflect.DeepEqual(items, want) {
			t.Errorf("branchItems = %+v, want %+v", items, want)
		}
	})

	t.Run("long subjects are truncated", func(t *testing.T) {
		subject := ""
		for range 200 {
			subject += "x"
		}
		item := branchItems([]Branch{{"b", 100, "now", subject, ""}}, map[string]Reason{"b": Merged})[0]
		if runes := []rune(item.Detail); len(runes) != subjectWidth || runes[len(runes)-1] != '…' {
			t.Errorf("Detail = %q", item.Detail)
		}
	})
}
