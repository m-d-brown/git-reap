package main

import (
	"reflect"
	"strings"
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
		upstream := ""
		if track != "" {
			upstream = "origin/" + name
		}
		return Branch{name, committedAt, "1 day ago", "s", upstream, track}
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
			// A deleted upstream says more about why the branch is finished.
			name:     "gone wins over merged and unused",
			branches: []Branch{branch("squashed", 10, "[gone]")},
			merged:   set("squashed"),
			want:     map[string]Reason{"squashed": Gone},
		},
		{
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
		protected := map[string]string{"main": "the base"}
		got := classifyBranches([]Branch{branch("main", 10, "[gone]")}, nil, protected, staleBefore)
		if len(got) != 0 {
			t.Errorf("classifyBranches = %v, want empty", got)
		}
	})

	// The half --debug prints. Every branch that is not a candidate gets an
	// explanation, and a protected one names which protection applied.
	t.Run("branches that are passed over say why", func(t *testing.T) {
		protected := map[string]string{"main": "the base"}
		if _, why := classify(branch("main", 10, "[gone]"), nil, protected, staleBefore); why != "protected: the base" {
			t.Errorf("classify(main) = %q, want the protection reason", why)
		}
		if _, why := classify(branch("active", 100, ""), nil, nil, staleBefore); !strings.Contains(why, "not merged") {
			t.Errorf("classify(active) = %q, want a reason mentioning the merge", why)
		}
		if reason, why := classify(branch("done", 100, ""), set("done"), nil, staleBefore); reason != Merged || why != "" {
			t.Errorf("classify(done) = %q/%q, want merged and no explanation", reason, why)
		}
	})
}

func TestPlanWorktrees(t *testing.T) {
	mainWorktree := Worktree{"/repo", "aaa", "main", false}
	// The ordinary case: the worktree's commits are in the base already, and it
	// was last used when that commit was written. The tests that care about
	// either of those coming apart say so.
	state := func(exists, dirty bool, committedAt int64) State {
		count := 0
		if dirty {
			count = 3
		}
		return State{
			Exists: exists, DirtyCount: count, Dirty: dirty,
			CommittedAt: committedAt, Relative: "1 day ago",
			TouchedAt: committedAt, TouchedRelative: "1 day ago",
			InBase: true,
		}
	}
	// outsideBase is the same worktree with its HEAD not contained in the base,
	// so removing it would orphan whatever it points at.
	outsideBase := func(s State) State {
		s.InBase = false
		return s
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
		want := Item{WorktreeKind, "/wt", Merged, "1 day ago", "clean", "done", false, false}
		if len(items) != 1 || items[0] != want {
			t.Fatalf("items = %+v, want [%+v]", items, want)
		}
		if len(kept) != 0 {
			t.Errorf("kept = %+v, want nothing", kept)
		}
	})

	t.Run("a worktree on a branch is never risky itself", func(t *testing.T) {
		// The branch outlives the worktree unless it is picked too, and its own
		// row carries whatever risk it has.
		items, _ := plan(
			Worktree{"/wt", "bbb", "done", false},
			outsideBase(state(true, false, 100)),
			map[string]Reason{"done": Merged},
			"/repo",
		)
		if items[0].Risky || items[0].State != "clean" {
			t.Errorf("items[0] = %+v, want not risky", items[0])
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
		if items[0].Risky || items[0].State != "clean" {
			t.Errorf("items[0] = %+v, want not risky when the HEAD is in the base", items[0])
		}
	})

	t.Run("detached worktree outside the base is risky", func(t *testing.T) {
		// Nothing but the worktree points at these commits, so removing it is
		// what orphans them.
		items, _ := plan(
			Worktree{"/wt", "0123456789abcdef", "", false},
			outsideBase(state(true, false, 10)),
			nil,
			"/repo",
		)
		if len(items) != 1 || !items[0].Risky || items[0].State != "only here" {
			t.Errorf("items = %+v, want a risky item", items)
		}
	})

	// The two clocks a detached worktree has are the commit under it and the
	// worktree itself, and only the second one answers "is anyone still working
	// here". Agent tooling parks worktrees on whatever commit it branched from,
	// so the first is routinely ancient on a worktree made minutes ago.
	t.Run("a fresh detached worktree on an old commit is kept", func(t *testing.T) {
		parked := state(true, false, 10)
		parked.TouchedAt, parked.TouchedRelative = 100, "1 minute ago"
		items, kept := plan(Worktree{"/wt", "abc", "", false}, parked, nil, "/repo")
		if len(items) != 0 {
			t.Errorf("items = %+v, want nothing: the worktree is in use", items)
		}
		if len(kept) != 1 || kept[0].Reason != "detached but recent" {
			t.Errorf("kept = %+v, want it kept as recent", kept)
		}
	})

	t.Run("an abandoned detached worktree on a new commit is offered", func(t *testing.T) {
		abandoned := state(true, false, 100)
		abandoned.TouchedAt, abandoned.TouchedRelative = 10, "6 months ago"
		items, _ := plan(Worktree{"/wt", "abc", "", false}, abandoned, nil, "/repo")
		if len(items) != 1 || items[0].Reason != Detached {
			t.Fatalf("items = %+v, want it offered as detached", items)
		}
		// The age shown is the one the decision turned on.
		if items[0].Age != "6 months ago" {
			t.Errorf("Age = %q, want the last-used age", items[0].Age)
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
			want:     Kept{WorktreeKind, "/wt", "detached but recent"},
		},
		{
			name:       "dirty worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(true, true, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{WorktreeKind, "/wt", "3 uncommitted files"},
		},
		{
			name:       "locked worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", true},
			state:      state(true, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{WorktreeKind, "/wt", "locked"},
		},
		{
			name:       "current worktree is kept",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(true, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/wt",
			want:       Kept{WorktreeKind, "/wt", "current"},
		},
		{
			name:       "missing directory is left to prune",
			worktree:   Worktree{"/wt", "b", "done", false},
			state:      state(false, false, 100),
			candidates: map[string]Reason{"done": Merged},
			current:    "/repo",
			want:       Kept{WorktreeKind, "/wt", "directory is gone; prune drops it"},
		},
		{
			// prune skips locked worktrees, so a locked entry survives even
			// when its directory is missing.
			name:     "locked is checked before the directory",
			worktree: Worktree{"/wt", "b", "done", true},
			state:    state(false, false, 100),
			current:  "/repo",
			want:     Kept{WorktreeKind, "/wt", "locked"},
		},
		{
			name:     "worktree on a branch still in use is kept",
			worktree: Worktree{"/wt", "b", "active", false},
			state:    state(true, false, 100),
			current:  "/repo",
			want:     Kept{WorktreeKind, "/wt", "active still in use"},
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

func TestPinBranches(t *testing.T) {
	mainWorktree := Worktree{"/repo", "aaa", "main", false}
	holder := Worktree{"/repo/wt", "bbb", "done", false}
	candidates := map[string]Reason{"done": Merged}

	t.Run("a branch held by a kept worktree is pinned", func(t *testing.T) {
		kept := []Kept{{WorktreeKind, "/repo/wt", "3 uncommitted files"}}
		got := pinBranches(candidates, []Worktree{mainWorktree, holder}, kept, "/repo")
		want := []Kept{{BranchKind, "done", "checked out at wt, which is kept: 3 uncommitted files"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("pinBranches = %+v, want %+v", got, want)
		}
	})

	t.Run("a branch whose worktree is offered stays a candidate", func(t *testing.T) {
		// Taking both rows frees the branch, so it is a real choice to offer.
		if got := pinBranches(candidates, []Worktree{mainWorktree, holder}, nil, "/repo"); len(got) != 0 {
			t.Errorf("pinBranches = %+v, want nothing", got)
		}
	})

	t.Run("a branch no worktree holds is untouched", func(t *testing.T) {
		kept := []Kept{{WorktreeKind, "/repo/other", "locked"}}
		other := Worktree{"/repo/other", "ccc", "unrelated", false}
		if got := pinBranches(candidates, []Worktree{mainWorktree, other}, kept, "/repo"); len(got) != 0 {
			t.Errorf("pinBranches = %+v, want nothing", got)
		}
	})

	t.Run("a worktree holding something we were not offering is ignored", func(t *testing.T) {
		kept := []Kept{{WorktreeKind, "/repo/wt", "locked"}}
		active := Worktree{"/repo/wt", "bbb", "active", false}
		if got := pinBranches(candidates, []Worktree{mainWorktree, active}, kept, "/repo"); len(got) != 0 {
			t.Errorf("pinBranches = %+v, want nothing", got)
		}
	})

	t.Run("the reason quotes whatever kept the worktree", func(t *testing.T) {
		kept := []Kept{{WorktreeKind, "/repo/wt", "locked"}}
		got := pinBranches(candidates, []Worktree{mainWorktree, holder}, kept, "/repo")
		if len(got) != 1 || !strings.HasSuffix(got[0].Reason, "which is kept: locked") {
			t.Errorf("pinBranches = %+v, want the holder's own reason", got)
		}
	})
}

func TestUncommitted(t *testing.T) {
	if got := uncommitted(1); got != "1 uncommitted file" {
		t.Errorf("uncommitted(1) = %q", got)
	}
	if got := uncommitted(3); got != "3 uncommitted files" {
		t.Errorf("uncommitted(3) = %q", got)
	}
}

func TestHumanizeSince(t *testing.T) {
	const now = 1_000_000_000
	tests := []struct {
		seconds int64
		want    string
	}{
		{0, "0 seconds ago"},
		{1, "1 second ago"},
		{45, "45 seconds ago"},
		{60, "1 minute ago"},
		{3599, "59 minutes ago"},
		{3600, "1 hour ago"},
		{5 * 3600, "5 hours ago"},
		{secondsPerDay, "1 day ago"},
		{13 * secondsPerDay, "13 days ago"},
		{14 * secondsPerDay, "2 weeks ago"},
		{69 * secondsPerDay, "9 weeks ago"},
		{70 * secondsPerDay, "2 months ago"},
		{364 * secondsPerDay, "12 months ago"},
		{365 * secondsPerDay, "1 year ago"},
		{800 * secondsPerDay, "2 years ago"},
	}
	for _, test := range tests {
		if got := humanizeSince(now-test.seconds, now); got != test.want {
			t.Errorf("humanizeSince(-%ds) = %q, want %q", test.seconds, got, test.want)
		}
	}

	t.Run("a timestamp we could not read is unknown", func(t *testing.T) {
		if got := humanizeSince(0, now); got != "unknown" {
			t.Errorf("humanizeSince(0) = %q, want unknown", got)
		}
	})

	t.Run("a clock skewed into the future reads as now", func(t *testing.T) {
		if got := humanizeSince(now+500, now); got != "0 seconds ago" {
			t.Errorf("humanizeSince(future) = %q", got)
		}
	})
}

func TestBranchItems(t *testing.T) {
	t.Run("items are in name order with metadata", func(t *testing.T) {
		branches := []Branch{
			{"zeta", 100, "2 days ago", "later work", "origin/zeta", "[gone]"},
			{"alpha", 100, "3 weeks ago", "early work", "", ""},
		}
		items := branchItems(branches, map[string]Reason{"zeta": Gone, "alpha": Merged}, set("alpha"), set("alpha"))
		want := []Item{
			{BranchKind, "alpha", Merged, "3 weeks ago", "no upstream", "early work", false, false},
			{BranchKind, "zeta", Gone, "2 days ago", "only here", "later work", true, true},
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
		branches := []Branch{{"b", 100, "now", subject, "", ""}}
		item := branchItems(branches, map[string]Reason{"b": Merged}, set("b"), set("b"))[0]
		if runes := []rune(item.Detail); len(runes) != subjectWidth || runes[len(runes)-1] != '…' {
			t.Errorf("Detail = %q", item.Detail)
		}
	})
}

// TestNeedsForce covers the bug this whole distinction exists for: `git branch
// -d` measures a branch against its upstream, or against HEAD when it has none,
// and neither of those is the base that decided the branch was merged.
func TestNeedsForce(t *testing.T) {
	tests := []struct {
		name         string
		branch       Branch
		mergedToHead map[string]bool
		want         bool
	}{
		{
			// The reported failure: merged into origin/main, but carrying
			// commits origin/restyle has never seen, so -d refuses it.
			name:         "ahead of its upstream",
			branch:       Branch{Name: "restyle", Upstream: "origin/restyle", Track: "[ahead 1, behind 9]"},
			mergedToHead: set("restyle"),
			want:         true,
		},
		{
			name:   "level with its upstream",
			branch: Branch{Name: "done", Upstream: "origin/done"},
			want:   false,
		},
		{
			name:   "behind its upstream only",
			branch: Branch{Name: "done", Upstream: "origin/done", Track: "[behind 9]"},
			want:   false,
		},
		{
			// git has no upstream left to compare against.
			name:   "upstream gone",
			branch: Branch{Name: "squashed", Upstream: "origin/squashed", Track: "[gone]"},
			want:   true,
		},
		{
			name:         "no upstream, merged into HEAD",
			branch:       Branch{Name: "local"},
			mergedToHead: set("local"),
			want:         false,
		},
		{
			// Merged into the base, but you are standing somewhere else: the
			// same failure, reached the other way.
			name:         "no upstream, not merged into HEAD",
			branch:       Branch{Name: "local"},
			mergedToHead: set("something-else"),
			want:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := needsForce(test.branch, test.mergedToHead); got != test.want {
				t.Errorf("needsForce = %v, want %v", got, test.want)
			}
		})
	}
}

// TestTrackState covers the state column, which is what should have warned that
// these branches held commits their own remote did not.
func TestTrackState(t *testing.T) {
	tests := []struct {
		name     string
		upstream string
		track    string
		inBase   bool
		want     string
		risky    bool
	}{
		{name: "no upstream but merged", inBase: true, want: "no upstream"},
		{name: "level with upstream", upstream: "origin/b", inBase: true, want: "pushed"},
		{name: "behind only", upstream: "origin/b", track: "[behind 9]", inBase: true, want: "pushed"},
		{name: "behind only, not in base", upstream: "origin/b", track: "[behind 9]", want: "pushed"},
		{
			name: "upstream gone but merged", upstream: "origin/b", track: "[gone]",
			inBase: true, want: "upstream gone",
		},
		{
			// The reported case: safe, because the commits are in the base.
			name: "ahead but merged", upstream: "origin/b", track: "[ahead 1, behind 9]",
			inBase: true, want: "1 unpushed",
		},
		{name: "no upstream and not merged", want: "only here", risky: true},
		{name: "upstream gone and not merged", upstream: "origin/b", track: "[gone]", want: "only here", risky: true},
		{
			name: "ahead and not merged", upstream: "origin/b", track: "[ahead 3]",
			want: "3 only here", risky: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			branch := Branch{Name: "b", Upstream: test.upstream, Track: test.track}
			if got := trackState(branch, test.inBase); got != test.want {
				t.Errorf("trackState = %q, want %q", got, test.want)
			}
			if got := onlyHere(branch, test.inBase); got != test.risky {
				t.Errorf("onlyHere = %v, want %v", got, test.risky)
			}
		})
	}
}

func TestUnpushed(t *testing.T) {
	tests := map[string]int{
		"":                    0,
		"[gone]":              0,
		"[behind 9]":          0,
		"[ahead 3]":           3,
		"[ahead 1, behind 9]": 1,
		"[ahead 12]":          12,
	}
	for track, want := range tests {
		if got := unpushed(track); got != want {
			t.Errorf("unpushed(%q) = %d, want %d", track, got, want)
		}
	}
}

func TestRiskSummary(t *testing.T) {
	risky := Item{Kind: BranchKind, Key: "a", Risky: true}
	safe := Item{Kind: BranchKind, Key: "b"}

	t.Run("nothing risky says nothing", func(t *testing.T) {
		if got := riskSummary([]Item{safe, safe}, "origin/main"); got != "" {
			t.Errorf("riskSummary = %q, want empty", got)
		}
	})

	t.Run("counts only the risky rows, and names the base", func(t *testing.T) {
		got := riskSummary([]Item{risky, safe, risky}, "origin/main")
		if !strings.HasPrefix(got, "2 rows are") || !strings.Contains(got, "origin/main") {
			t.Errorf("riskSummary = %q", got)
		}
	})

	t.Run("one row reads as one row", func(t *testing.T) {
		if got := riskSummary([]Item{risky, safe}, "main"); !strings.HasPrefix(got, "1 row is") {
			t.Errorf("riskSummary = %q", got)
		}
	})
}
