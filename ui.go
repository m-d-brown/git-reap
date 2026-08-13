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

// selectWithFzf shows rows in fzf and returns the tokens picked. The second
// result is false when the selection was cancelled.
func selectWithFzf(rows []string, fzf, preview string) ([]string, bool) {
	command := exec.Command(fzf,
		"--multi",
		"--delimiter", field,
		// Show only the display half, but keep the token in the line so the
		// selection and the preview can both find it.
		"--with-nth", "2..",
		"--header", "TAB to mark, Enter to delete the marked rows, Esc to cancel",
		"--preview", preview+" {1}",
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

// renderPreview describes one candidate in detail, for the fzf preview pane.
func renderPreview(token string) string {
	kind, key, _ := strings.Cut(token, ":")
	if kind == string(WorktreeKind) {
		head := gitTry("-C", key, "rev-parse", "HEAD")
		status := gitTry("-C", key, "status", "--short", "--branch")
		log := gitTry("-C", key, "log", "-10", "--format=%h %cr  %s")
		return key + "\n\nHEAD " + head + "\n\n" + status + "\n\n" + log
	}

	upstream := gitTry("for-each-ref", "--format=%(upstream:short) %(upstream:track)", "refs/heads/"+key)
	if strings.TrimSpace(upstream) == "" {
		upstream = "none"
	}
	where := ""
	if holder := worktreeHolding(key); holder != "" {
		where = "\nchecked out at " + holder
	}
	log := gitTry("log", "-10", "--format=%h %cr  %an  %s", key)
	return key + "\n\nupstream: " + strings.TrimSpace(upstream) + where + "\n\n" + log
}
