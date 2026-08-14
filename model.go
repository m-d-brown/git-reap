package main

import (
	"strconv"
	"strings"
)

// field separates the columns of git's --format output, and the token from the
// display half of an fzf row. \x1f is the ASCII unit separator, which cannot
// appear in a ref name and will not realistically appear in a commit subject --
// unlike a tab.
const field = "\x1f"

// branchFormat drives `git for-each-ref`. The tracking column comes last
// because it is empty for a branch with no upstream, and git strips trailing
// separators off the final line of output.
var branchFormat = strings.Join([]string{
	"%(refname:short)",
	"%(committerdate:unix)",
	"%(committerdate:relative)",
	"%(subject)",
	"%(upstream:short)",
	"%(upstream:track)",
}, field)

// branchColumns is how many fields branchFormat produces.
const branchColumns = 6

// Reason says why something qualifies for deletion.
type Reason string

const (
	Merged   Reason = "merged"
	Gone     Reason = "upstream gone"
	Unused   Reason = "unused"
	Detached Reason = "detached"
)

// Kind distinguishes the two things we delete.
type Kind string

const (
	BranchKind   Kind = "branch"
	WorktreeKind Kind = "worktree"
)

// Branch is a local branch, as reported by `git for-each-ref refs/heads`.
type Branch struct {
	Name        string
	CommittedAt int64
	Relative    string
	Subject     string
	// Upstream is the remote-tracking branch, "origin/x", or "" when the branch
	// has none. Track is empty in both of those cases, so this is the only
	// thing that tells "in sync" apart from "never had an upstream".
	Upstream string
	// Track is the upstream-tracking text: "[gone]", "[ahead 2]", or "" when
	// the branch has no upstream or is level with it.
	Track string
}

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path string
	Head string
	// Branch is empty when the worktree is on a detached HEAD.
	Branch string
	Locked bool
}

// State is what the working copy of a worktree looks like right now. It is
// gathered separately from the porcelain listing so that the planning below
// stays a pure function of its inputs.
type State struct {
	Exists      bool
	Dirty       bool
	CommittedAt int64
	Relative    string
	// InBase says whether the worktree's HEAD is contained in the base branch.
	// A detached worktree whose HEAD is not carries commits that removing it
	// would orphan.
	InBase bool
}

// Item is one deletion candidate, ready to display, select, and act on.
type Item struct {
	Kind   Kind
	Key    string
	Reason Reason
	// Display columns, in order: age, state, detail.
	Age    string
	State  string
	Detail string
	// Force says that `git branch -d` would refuse this branch, so -D is what
	// actually deletes it. Worked out from what git itself checks, not from
	// Reason -- see needsForce.
	Force bool
	// Risky says the commits here are in neither the base nor any remote, so
	// deleting really does drop them. Merged branches are never risky, however
	// much their own remote branch has fallen behind.
	Risky bool
}

// Token is the stable id round-tripped through fzf and --preview.
func (i Item) Token() string { return string(i.Kind) + ":" + i.Key }

// Kept is a worktree we left alone, and why, so --dry-run can explain itself.
type Kept struct {
	Path   string
	Reason string
}

// parseBranches parses the output of `git for-each-ref --format=branchFormat`.
func parseBranches(output string) []Branch {
	var branches []Branch
	for _, line := range lines(output) {
		columns := strings.SplitN(line, field, branchColumns)
		// The upstream columns are missing rather than empty on the last line,
		// whose trailing separators gitCapture has stripped.
		for len(columns) < branchColumns {
			columns = append(columns, "")
		}
		committedAt, _ := strconv.ParseInt(columns[1], 10, 64)
		branches = append(branches, Branch{
			Name:        columns[0],
			CommittedAt: committedAt,
			Relative:    columns[2],
			Subject:     columns[3],
			Upstream:    columns[4],
			Track:       columns[5],
		})
	}
	return branches
}

// parseWorktrees parses `git worktree list --porcelain`.
//
// Each worktree is a block opened by "worktree <path>", followed by
// "HEAD <sha>" and either "branch <ref>" or "detached", plus "locked" when it
// is locked. The first block is the main worktree, which must never be removed.
func parseWorktrees(porcelain string) []Worktree {
	var (
		worktrees []Worktree
		current   *Worktree
	)
	flush := func() {
		if current != nil {
			worktrees = append(worktrees, *current)
		}
	}
	for _, line := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case current == nil:
			// Anything before the first block has no worktree to belong to.
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		}
	}
	flush()
	return worktrees
}

// lines splits text into its non-empty lines.
func lines(text string) []string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return kept
}
