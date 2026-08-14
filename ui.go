package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// formatRows renders the items as aligned fzf rows.
//
// Each row is "<token><field><display>", so fzf can show the display half
// (--with-nth=2..) while the selection still carries the token.
func formatRows(items []Item, root string) []string {
	columns := make([][]string, len(items))
	widths := make([]int, 5)
	for i, item := range items {
		key := item.Key
		if item.Kind == WorktreeKind && root != "" && strings.HasPrefix(key, root+string(filepath.Separator)) {
			if relative, err := filepath.Rel(root, key); err == nil {
				key = relative
			}
		}
		columns[i] = []string{string(item.Kind), key, string(item.Reason), item.Age, item.State, item.Detail}
		for c, text := range columns[i][:5] {
			widths[c] = max(widths[c], utf8.RuneCountInString(text))
		}
	}

	rows := make([]string, len(items))
	for i, item := range items {
		padded := make([]string, 5)
		for c, text := range columns[i][:5] {
			padded[c] = text + strings.Repeat(" ", widths[c]-utf8.RuneCountInString(text))
		}
		row := item.Token() + field + strings.Join(padded, "  ") + "  " + columns[i][5]
		rows[i] = strings.TrimRight(row, " ")
	}
	return rows
}

// display drops the token from a row, leaving the half meant to be read.
func display(row string) string {
	_, shown, _ := strings.Cut(row, field)
	return shown
}

// parseSelection pulls the tokens back out of the rows fzf printed.
func parseSelection(output string) []string {
	tokens := []string{}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			token, _, _ := strings.Cut(line, field)
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// keys is the picker's whole header. The count of rows that are "only here"
// used to hang a second line under it, but the preview pane already says the
// same thing about the row under the cursor, and says it with the real number
// of commits -- so the header spent a line teaching a term that the pane two
// lines below was already defining. --dry-run and the --all confirmation, which
// have no preview pane, still carry the summary.
const keys = "TAB to mark, Enter to delete the marked rows, Esc to cancel"

// selectWithFzf shows rows in fzf and returns the tokens picked. The second
// result is false when the selection was cancelled.
func selectWithFzf(rows []string, fzf, preview, header string) ([]string, bool) {
	command := exec.Command(fzf,
		"--multi",
		"--delimiter", field,
		// Show only the display half, but keep the token in the line so the
		// selection and the preview can both find it.
		"--with-nth", "2..",
		"--header", header,
		"--preview", preview,
		"--preview-window", "down,60%,wrap",
	)
	command.Stdin = strings.NewReader(strings.Join(rows, "\n"))
	command.Stderr = os.Stderr
	output, err := command.Output()
	// fzf exits 130 when the selection was cancelled, and 1 when nothing matched.
	if err != nil {
		return nil, false
	}
	return parseSelection(string(output)), true
}

// renderPreview describes one candidate in detail, for the fzf preview pane. It
// leads with where the commits live, because that is the one thing worth
// knowing before marking a row.
func renderPreview(token, base string) string {
	kind, key, _ := strings.Cut(token, ":")
	if kind == string(WorktreeKind) {
		head := gitTry("-C", key, "rev-parse", "HEAD")
		lead := whereTheCommitsLive(head, "", "", base)
		if branch := gitTry("-C", key, "symbolic-ref", "--quiet", "--short", "HEAD"); branch != "" {
			lead = "on " + branch + "; removing the worktree leaves the branch alone"
		}
		status := gitTry("-C", key, "status", "--short", "--branch")
		log := gitTry("-C", key, "log", "-10", "--format=%h %cr  %s")
		return key + "\n\n" + lead + "\n\nHEAD " + head + "\n\n" + status + "\n\n" + log
	}

	tracking := gitTry("for-each-ref", "--format=%(upstream:short)"+field+"%(upstream:track)", "refs/heads/"+key)
	upstream, track, _ := strings.Cut(tracking, field)
	shown := strings.TrimSpace(upstream + " " + track)
	if shown == "" {
		shown = "none"
	}
	where := ""
	if holder := worktreeHolding(key); holder != "" {
		where = "\nchecked out at " + holder
	}
	log := gitTry("log", "-10", "--format=%h %cr  %an  %s", key)
	return key + "\n\n" + whereTheCommitsLive(key, upstream, track, base) +
		"\nupstream: " + shown + where + "\n\n" + log
}

// whereTheCommitsLive says whether deleting rev can lose anything, in the same
// words the rows and the header use. Being contained in the base is what makes
// a deletion free, however far the branch's own remote has fallen behind --
// that gap costs a -D, not any commits.
func whereTheCommitsLive(rev, upstream, track, base string) string {
	if gitSucceeds("merge-base", "--is-ancestor", rev, base) {
		return "in " + base + "; deleting drops nothing"
	}
	pushed := upstream != "" && !strings.Contains(track, "gone")
	if pushed && unpushed(track) == 0 {
		return "not in " + base + ", but pushed to " + upstream
	}
	// Everything reachable from here that is in neither place is what goes.
	args := []string{"rev-list", "--count", rev, "--not", base}
	if pushed {
		args = append(args, upstream)
	}
	count := gitTry(args...)
	if count == "" {
		count = "?"
	}
	commits := " commits"
	if count == "1" {
		commits = " commit"
	}
	return red("only here: not in " + base + " and on no remote -- deleting drops " +
		count + commits + ", which only the reflog would then hold")
}

// red marks the one line in the preview worth stopping on. The colour is not
// gated on stdout being a terminal, the way such a check usually goes: --preview
// runs as a child of fzf with a pipe for stdout, so a tty test would strip the
// colour in the only place it is ever printed. NO_COLOR turns it off instead.
func red(text string) string {
	if os.Getenv("NO_COLOR") != "" {
		return text
	}
	return "\033[31m" + text + "\033[0m"
}
