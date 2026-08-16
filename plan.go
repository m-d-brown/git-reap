package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// subjectWidth is how much of a commit subject a branch row shows.
const subjectWidth = 50

// classify decides whether one branch qualifies for deletion, and says why not
// when it does not. Exactly one of the results is set: a Reason when the branch
// is a candidate, an explanation when it is passed over.
//
// The order matters, because a branch can qualify several ways over and the
// reason is what the row reports: an upstream that was deleted says more about
// why the branch is finished than the merge does, and both say more than the
// branch merely having gone quiet.
//
// The explanation is the half --debug prints. It comes from here, rather than
// from a second pass that re-derives it, so that what --debug says about a
// branch cannot drift from what actually happened to it. protected maps a
// branch name to the reason it is untouchable, which is why it is not a plain
// set: "why is main never offered" deserves an answer.
func classify(branch Branch, merged map[string]bool, protected map[string]string, staleBefore int64) (Reason, string) {
	switch {
	case protected[branch.Name] != "":
		return "", "protected: " + protected[branch.Name]
	case strings.Contains(branch.Track, "gone"):
		return Gone, ""
	case merged[branch.Name]:
		return Merged, ""
	case branch.CommittedAt < staleBefore:
		return Unused, ""
	default:
		return "", "not merged, upstream not gone, last commit " + branch.Relative
	}
}

// classifyBranches picks out the branches that qualify, mapped to why they do.
func classifyBranches(branches []Branch, merged map[string]bool, protected map[string]string, staleBefore int64) map[string]Reason {
	candidates := map[string]Reason{}
	for _, branch := range branches {
		if reason, _ := classify(branch, merged, protected, staleBefore); reason != "" {
			candidates[branch.Name] = reason
		}
	}
	return candidates
}

// needsForce reports whether `git branch -d` would refuse this branch, so that
// -D is what actually deletes it.
//
// git's own check asks whether the branch is contained in its upstream -- or in
// HEAD, when there is no upstream -- and neither of those is the base we
// measured "merged" against. A branch sitting in origin/main but ahead of its
// own remote branch fails git's check even though deleting it loses nothing,
// which is the whole reason this is worked out up front rather than guessed
// from the reason.
func needsForce(branch Branch, mergedToHead map[string]bool) bool {
	switch {
	case strings.Contains(branch.Track, "gone"):
		// The upstream git would compare against is not there any more.
		return true
	case branch.Upstream == "":
		return !mergedToHead[branch.Name]
	default:
		return unpushed(branch.Track) > 0
	}
}

// onlyHere reports that deleting this branch drops commits that exist nowhere
// else: not in the base, and not on a remote branch either. It is the state
// worth being loud about, and it is not the state that needs -D -- a merged
// branch can need forcing and still be perfectly safe to delete.
func onlyHere(branch Branch, inBase bool) bool {
	if inBase {
		return false
	}
	return branch.Upstream == "" ||
		strings.Contains(branch.Track, "gone") ||
		unpushed(branch.Track) > 0
}

// trackState is a branch's state column: where its commits live, in plain
// words, so a row says by itself whether deleting it can lose anything.
func trackState(branch Branch, inBase bool) string {
	ahead := unpushed(branch.Track)
	if onlyHere(branch, inBase) {
		if ahead > 0 {
			return strconv.Itoa(ahead) + " only here"
		}
		return "only here"
	}
	switch {
	case branch.Upstream == "":
		return "no upstream"
	case strings.Contains(branch.Track, "gone"):
		return "upstream gone"
	case ahead > 0:
		// In the base, so these commits are safe; just not on origin/<branch>.
		return strconv.Itoa(ahead) + " unpushed"
	default:
		return "pushed"
	}
}

// unpushed is how many commits the branch has that its upstream does not,
// read out of the text git renders as "[ahead 3, behind 1]".
func unpushed(track string) int {
	_, rest, found := strings.Cut(track, "ahead ")
	if !found {
		return 0
	}
	digits, _, _ := strings.Cut(rest, ",")
	digits, _, _ = strings.Cut(digits, "]")
	count, _ := strconv.Atoi(digits)
	return count
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
		var risky bool
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
			// Nothing but this worktree points at these commits, so removing it
			// is what orphans them.
			reason, detail, risky = Detached, "detached at "+abbreviate(worktree.Head), !state.InBase
		default:
			qualifying, ok := candidates[worktree.Branch]
			if !ok {
				keep(worktree.Path, worktree.Branch+" still in use")
				continue
			}
			// The branch outlives the worktree unless it is picked too, and its
			// own row carries whatever risk it has.
			reason, detail = qualifying, worktree.Branch
		}

		display := "clean"
		if risky {
			display = "only here"
		}
		items = append(items, Item{
			Kind:   WorktreeKind,
			Key:    worktree.Path,
			Reason: reason,
			Age:    state.Relative,
			State:  display,
			Detail: detail,
			Risky:  risky,
		})
	}
	return items, kept
}

// branchItems builds the display items for the qualifying branches, in name
// order. merged says which branches are contained in the base, and mergedToHead
// which are contained in HEAD; between them they settle how a branch is
// deleted and whether deleting it can lose anything.
func branchItems(branches []Branch, candidates map[string]Reason, merged, mergedToHead map[string]bool) []Item {
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
		inBase := merged[name]
		items = append(items, Item{
			Kind:   BranchKind,
			Key:    name,
			Reason: candidates[name],
			Age:    branch.Relative,
			State:  trackState(branch, inBase),
			Detail: truncate(branch.Subject, subjectWidth),
			Force:  needsForce(branch, mergedToHead),
			Risky:  onlyHere(branch, inBase),
		})
	}
	return items
}

// riskyCount is how many of the items hold commits that are only here.
func riskyCount(items []Item) int {
	count := 0
	for _, item := range items {
		if item.Risky {
			count++
		}
	}
	return count
}

// riskSummary is the one line that every summary of a run shares: the picker's
// header, the --all confirmation, and the tail of --dry-run. It is empty when
// nothing on offer holds commits of its own.
func riskSummary(items []Item, base string) string {
	count := riskyCount(items)
	if count == 0 {
		return ""
	}
	rows := "rows are"
	if count == 1 {
		rows = "row is"
	}
	return fmt.Sprintf("%d %s \"only here\": not in %s and on no remote -- deleting drops those commits",
		count, rows, base)
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
