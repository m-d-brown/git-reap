package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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
	keep := func(path, why string) {
		kept = append(kept, Kept{Kind: WorktreeKind, Name: path, Reason: why})
	}

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
			keep(worktree.Path, uncommitted(state.DirtyCount))
			continue
		case worktree.Branch == "":
			// Detached and clean: only a candidate once it has gone quiet,
			// since there is no branch to tell us whether it is finished.
			//
			// Quiet means the worktree has not been used, not that the commit
			// under it is old. Agent tooling parks these on whatever commit it
			// branched from, so the commit date says when that ancestor was
			// written and nothing at all about whether anyone is still working
			// here -- reading it would offer a worktree made minutes ago.
			if state.TouchedAt >= staleBefore {
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
		// A detached row is offered on the strength of when it was last used, so
		// that is the age it shows; a worktree on a branch is describing the
		// branch's last commit, which is what its own row would say.
		age := state.Relative
		if worktree.Branch == "" {
			age = state.TouchedRelative
		}
		items = append(items, Item{
			Kind:   WorktreeKind,
			Key:    worktree.Path,
			Reason: reason,
			Age:    age,
			State:  display,
			Detail: detail,
			Risky:  risky,
		})
	}
	return items, kept
}

// pinBranches finds the candidate branches that a worktree has checked out and
// will go on holding, because that worktree is one we are keeping.
//
// git refuses to delete such a branch, and execute would report it as kept once
// the picking was over -- so leaving them in the offered list means offering
// rows that cannot do anything, and counting deletions that will not happen.
// They belong with the worktrees we passed over, and for the same reason.
//
// The explanation quotes the holding worktree's own reason rather than working
// one out again, so the two lines of a --dry-run cannot end up disagreeing
// about why that worktree is still there.
func pinBranches(candidates map[string]Reason, worktrees []Worktree, kept []Kept, root string) []Kept {
	staying := map[string]string{}
	for _, keep := range kept {
		if keep.Kind == WorktreeKind {
			staying[keep.Name] = keep.Reason
		}
	}

	var pinned []Kept
	for _, worktree := range worktrees {
		if worktree.Branch == "" || candidates[worktree.Branch] == "" {
			continue
		}
		why, held := staying[worktree.Path]
		if !held {
			// The worktree is on offer too, so taking both frees the branch.
			continue
		}
		pinned = append(pinned, Kept{
			Kind:   BranchKind,
			Name:   worktree.Branch,
			Reason: "checked out at " + relativePath(worktree.Path, root) + ", which is kept: " + why,
		})
	}
	sort.Slice(pinned, func(a, b int) bool { return pinned[a].Name < pinned[b].Name })
	return pinned
}

// uncommitted describes how much work a dirty worktree is holding, which is the
// difference between one stray file and an afternoon's changes.
func uncommitted(count int) string {
	if count == 1 {
		return "1 uncommitted file"
	}
	return strconv.Itoa(count) + " uncommitted files"
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

// humanize renders a timestamp the way git renders %cr, which is what the
// column beside it holds. A file's mtime arrives as a bare instant, with none
// of the phrasing git hands over for free.
func humanize(unix int64) string {
	return humanizeSince(unix, time.Now().Unix())
}

// humanizeSince is humanize against a given now, which is what makes it
// testable.
//
// This is git's own show_date_relative, thresholds and rounding both, rather
// than an approximation of it. The two live side by side -- the debug report
// puts a %cr last-commit column next to a last-used one rendered here -- so an
// approximation would have the same instant reading "5 months ago" in one
// column and "4 months ago" in the next, which looks like a bug in the thing
// the columns are there to explain.
func humanizeSince(unix, now int64) string {
	if unix <= 0 {
		return "unknown"
	}
	diff := now - unix
	if diff < 0 {
		diff = 0
	}
	if diff < 90 {
		return plural(diff, "second") + " ago"
	}
	diff = (diff + 30) / 60 // minutes
	if diff < 90 {
		return plural(diff, "minute") + " ago"
	}
	diff = (diff + 30) / 60 // hours
	if diff < 36 {
		return plural(diff, "hour") + " ago"
	}
	diff = (diff + 12) / 24 // days from here on
	if diff < 14 {
		return plural(diff, "day") + " ago"
	}
	if diff < 70 {
		return plural((diff+3)/7, "week") + " ago"
	}
	if diff < 365 {
		return plural((diff+15)/30, "month") + " ago"
	}
	// Under five years git spells out the leftover months, since "1 year ago"
	// and "1 year, 11 months ago" are not the same news.
	if diff < 1825 {
		totalMonths := (diff*12*2 + 365) / (365 * 2)
		years, months := totalMonths/12, totalMonths%12
		if months > 0 {
			return plural(years, "year") + ", " + plural(months, "month") + " ago"
		}
		return plural(years, "year") + " ago"
	}
	return plural((diff+183)/365, "year") + " ago"
}

// plural counts a unit in words: "1 day", "3 days".
func plural(count int64, unit string) string {
	text := strconv.FormatInt(count, 10) + " " + unit
	if count == 1 {
		return text
	}
	return text + "s"
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
