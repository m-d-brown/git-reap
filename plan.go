package main

import (
	"sort"
	"strings"
)

// subjectWidth is how much of a commit subject a branch row shows.
const subjectWidth = 50

// classifyBranches picks out the branches that qualify, mapped to why they do.
//
// The order matters, because the reason decides the delete flag: gone and
// unused branches are not merged as far as git is concerned and need -D, while
// merged uses -d and keeps git's own safety check. A branch that is both merged
// and idle is reported as merged, the gentler of the two.
func classifyBranches(branches []Branch, merged, protected map[string]bool, staleBefore int64) map[string]Reason {
	candidates := map[string]Reason{}
	for _, branch := range branches {
		switch {
		case protected[branch.Name]:
		case strings.Contains(branch.Track, "gone"):
			candidates[branch.Name] = Gone
		case merged[branch.Name]:
			candidates[branch.Name] = Merged
		case branch.CommittedAt < staleBefore:
			candidates[branch.Name] = Unused
		}
	}
	return candidates
}

// planWorktrees splits the worktrees into deletion candidates and ones we leave
// alone. It returns the candidate items, plus the worktrees kept and why, so
// --dry-run can explain the omissions.
func planWorktrees(
	worktrees []Worktree,
	states map[string]State,
	candidates map[string]Reason,
	currentPath string,
	staleBefore int64,
) ([]Item, []Kept) {
	if len(worktrees) == 0 {
		return nil, nil
	}
	var (
		items []Item
		kept  []Kept
	)
	keep := func(path, why string) { kept = append(kept, Kept{Path: path, Reason: why}) }

	// worktrees[0] is the main worktree, which is not ours to remove.
	for _, worktree := range worktrees[1:] {
		state := states[worktree.Path]

		var reason Reason
		var detail string
		switch {
		case worktree.Locked:
			keep(worktree.Path, "locked")
			continue
		case worktree.Path == currentPath:
			keep(worktree.Path, "current")
			continue
		case !state.Exists:
			// `git worktree prune` deals with these, so there is no directory
			// for us to remove.
			keep(worktree.Path, "directory is gone; prune drops it")
			continue
		case state.Dirty:
			keep(worktree.Path, "uncommitted changes")
			continue
		case worktree.Branch == "":
			// Detached and clean: only a candidate once it has gone quiet,
			// since there is no branch to tell us whether it is finished.
			if state.CommittedAt >= staleBefore {
				keep(worktree.Path, "detached but recent")
				continue
			}
			reason, detail = Detached, "detached at "+abbreviate(worktree.Head)
		default:
			qualifying, ok := candidates[worktree.Branch]
			if !ok {
				keep(worktree.Path, worktree.Branch+" still in use")
				continue
			}
			reason, detail = qualifying, worktree.Branch
		}

		items = append(items, Item{
			Kind:   WorktreeKind,
			Key:    worktree.Path,
			Reason: reason,
			Age:    state.Relative,
			State:  "clean",
			Detail: detail,
		})
	}
	return items, kept
}

// branchItems builds the display items for the qualifying branches, in name
// order.
func branchItems(branches []Branch, candidates map[string]Reason) []Item {
	byName := make(map[string]Branch, len(branches))
	for _, branch := range branches {
		byName[branch.Name] = branch
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]Item, 0, len(names))
	for _, name := range names {
		branch := byName[name]
		state := branch.Track
		if state == "" {
			state = "no upstream"
		}
		items = append(items, Item{
			Kind:   BranchKind,
			Key:    name,
			Reason: candidates[name],
			Age:    branch.Relative,
			State:  state,
			Detail: truncate(branch.Subject, subjectWidth),
		})
	}
	return items
}

// abbreviate shortens a commit sha to the length git itself tends to show.
func abbreviate(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// truncate cuts text down to width characters, marking the cut with an ellipsis.
func truncate(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width-1]) + "…"
}
