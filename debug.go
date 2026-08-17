package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// debugState is everything a run worked out, handed to the report whole.
//
// It is deliberately the actual values the run decided from rather than a
// summary of them: --debug exists for the case where the answer is surprising,
// and a report that paraphrases its inputs cannot explain a surprise. Because
// the report is built from these after every decision has been made, it costs
// no extra git commands and cannot disagree with the run it describes.
type debugState struct {
	opts         options
	base, why    string
	root         string
	worktrees    []Worktree
	states       map[string]State
	branches     []Branch
	merged       map[string]bool
	mergedToHead map[string]bool
	protected    map[string]string
	staleBefore  int64
	items        []Item
	kept         []Kept
	// pinned maps a branch to the reason a worktree still holding it kept it
	// out of the run, which classify cannot work out on its own: it depends on
	// which worktrees survived.
	pinned map[string]string
}

// reportDebug prints the state behind every decision this run made: how the
// base resolved, what each worktree looks like on disk, where each branch's
// commits are, and -- the part worth having -- why each branch that was passed
// over was passed over.
func reportDebug(state debugState) {
	out := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(out, "## the run")
	fmt.Fprintf(out, "git\t%s\n", gitTry("--version"))
	fmt.Fprintf(out, "repository\t%s\n", state.root)
	if fzf, err := exec.LookPath("fzf"); err == nil {
		fmt.Fprintf(out, "fzf\t%s\n", fzf)
	} else {
		fmt.Fprintf(out, "fzf\tnot found -- the picker is unavailable, --dry-run and --all still work\n")
	}
	if state.opts.fetch {
		fmt.Fprintln(out, "fetch\tran git fetch --prune, so [gone] is current")
	} else {
		fmt.Fprintln(out, "fetch\tskipped (--no-fetch), so [gone] is only as fresh as the last fetch")
	}
	fmt.Fprintf(out, "days\t%d -- idle means before %s: for a branch its last commit, "+
		"for a detached worktree when it was last used\n",
		state.opts.days, time.Unix(state.staleBefore, 0).Format("2006-01-02 15:04"))
	fmt.Fprintln(out)

	fmt.Fprintln(out, "## the base")
	fmt.Fprintf(out, "%s\t%s\n", state.base, state.why)
	fmt.Fprintln(out, "\t'merged' below means contained in this ref.")
	fmt.Fprintln(out)

	reportWorktrees(out, state)
	reportBranches(out, state)
	reportOffered(out, state)

	out.Flush()
}

// reportWorktrees lists every worktree with the state gathered from disk and
// what the plan did with it. The main worktree is included, though it is never
// a candidate, because its branch is protected and that is worth seeing.
func reportWorktrees(out *tabwriter.Writer, state debugState) {
	offered := map[string]Item{}
	for _, item := range state.items {
		offered[item.Token()] = item
	}
	kept := map[string]string{}
	for _, keep := range state.kept {
		if keep.Kind == WorktreeKind {
			kept[keep.Name] = keep.Reason
		}
	}

	// Paths are relative to the root named at the top of the report, which is
	// the one place the report spells the repository out in full. Both clocks
	// are shown because the detached rule turns on the difference between them.
	fmt.Fprintln(out, "## worktrees")
	fmt.Fprintln(out, "path\ton\tstate\tlast commit\tlast used\toutcome")
	for i, worktree := range state.worktrees {
		on := worktree.Branch
		if on == "" {
			on = "detached at " + abbreviate(worktree.Head)
		}

		facts := []string{}
		current := state.states[worktree.Path]
		switch {
		case !current.Exists:
			facts = append(facts, "directory gone")
		default:
			if current.Dirty {
				facts = append(facts, "dirty ("+uncommitted(current.DirtyCount)+")")
			} else {
				facts = append(facts, "clean")
			}
			if current.InBase {
				facts = append(facts, "in base")
			} else {
				facts = append(facts, "not in base")
			}
		}
		if worktree.Locked {
			facts = append(facts, "locked")
		}

		outcome := ""
		switch item, isOffered := offered[string(WorktreeKind)+":"+worktree.Path]; {
		case i == 0:
			outcome = "the main worktree, never removed"
		case isOffered:
			outcome = "offered (" + string(item.Reason) + ")"
		case kept[worktree.Path] != "":
			outcome = "kept: " + kept[worktree.Path]
		default:
			outcome = "not offered"
		}

		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n",
			relativePath(worktree.Path, state.root), on, strings.Join(facts, ", "),
			current.Relative, current.TouchedRelative, outcome)
	}
	fmt.Fprintln(out)
}

// reportBranches lists every local branch with the refs the decision turns on,
// and what that decision was. The branches that are *not* candidates are the
// point of this section: a branch missing from a run is the thing --debug is
// usually being asked about, and every other view of the repository shows only
// the branches that made it through.
func reportBranches(out *tabwriter.Writer, state debugState) {
	fmt.Fprintln(out, "## branches")
	fmt.Fprintln(out, "branch\tupstream\ttrack\tin base\tin HEAD\toutcome")

	branches := append([]Branch(nil), state.branches...)
	sort.Slice(branches, func(a, b int) bool { return branches[a].Name < branches[b].Name })

	for _, branch := range branches {
		upstream := branch.Upstream
		if upstream == "" {
			upstream = "none"
		}
		track := branch.Track
		if track == "" {
			track = "-"
		}

		outcome := ""
		reason, why := classify(branch, state.merged, state.protected, state.staleBefore)
		switch {
		case reason == "":
			outcome = why
		case state.pinned[branch.Name] != "":
			// It qualified, and then the worktree holding it stayed, so it is
			// kept rather than offered. Naming the reason it qualified as well
			// keeps the branch worth coming back to once that worktree is dealt
			// with.
			outcome = "kept (" + string(reason) + "): " + state.pinned[branch.Name]
		default:
			outcome = "offered (" + string(reason) + ")"
			if holder := holderOf(branch.Name, state.worktrees); holder != "" {
				outcome += ", checked out at " + relativePath(holder, state.root)
			}
		}

		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", branch.Name, upstream, track,
			yesNo(state.merged[branch.Name]), yesNo(state.mergedToHead[branch.Name]), outcome)
	}
	fmt.Fprintln(out)
}

// reportOffered ends with the rows the run would actually have shown, so the
// report finishes on the thing the sections above explain.
func reportOffered(out *tabwriter.Writer, state debugState) {
	fmt.Fprintln(out, "## what this run offers")
	if len(state.items) == 0 {
		fmt.Fprintln(out, "nothing -- every branch and worktree above says why")
		return
	}
	for _, row := range formatRows(state.items, state.root) {
		fmt.Fprintln(out, strings.ReplaceAll(display(row), "\t", " "))
	}
	if risk := riskSummary(state.items, state.base); risk != "" {
		fmt.Fprintln(out, risk)
	}
}

// yesNo renders the two containment columns, which are the ones people squint
// at: "in base" decides merged, and "in HEAD" decides whether git's own -d
// would accept a branch that has no upstream.
func yesNo(yes bool) string {
	if yes {
		return "yes"
	}
	return "no"
}
