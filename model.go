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
	"%(upstream:track)",
}, field)

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
	// Track is the upstream-tracking text: "[gone]", "[ahead 2]", or "" when
	// the branch has no upstream.
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
		columns := strings.SplitN(line, field, 5)
		// The tracking column is missing rather than empty on the last line,
		// whose trailing separator gitCapture has stripped.
		for len(columns) < 5 {
			columns = append(columns, "")
		}
		committedAt, _ := strconv.ParseInt(columns[1], 10, 64)
		branches = append(branches, Branch{
			Name:        columns[0],
			CommittedAt: committedAt,
			Relative:    columns[2],
			Subject:     columns[3],
			Track:       columns[4],
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
