package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleItems() []Item {
	return []Item{
		{WorktreeKind, "/repo/wt", Detached, "3 months ago", "clean", "detached at abcdef12", false, false},
		{BranchKind, "feature", Gone, "2 days ago", "only here", "add a thing", true, true},
	}
}

func TestFormatRows(t *testing.T) {
	t.Run("token is the first column", func(t *testing.T) {
		rows := formatRows(sampleItems(), "")
		for i, want := range []string{"worktree:/repo/wt" + field, "branch:feature" + field} {
			if !strings.HasPrefix(rows[i], want) {
				t.Errorf("row %d = %q, want prefix %q", i, rows[i], want)
			}
		}
	})

	t.Run("worktree paths are shown relative to the repo", func(t *testing.T) {
		shown := display(formatRows(sampleItems(), "/repo")[0])
		if !strings.Contains(" "+shown+" ", " wt ") || strings.Contains(shown, "/repo/wt") {
			t.Errorf("display = %q, want the path relative to /repo", shown)
		}
	})

	t.Run("columns are aligned", func(t *testing.T) {
		rows := formatRows(sampleItems(), "")
		// The reason column starts at the same offset on every row.
		first := strings.Index(display(rows[0]), string(Detached))
		second := strings.Index(display(rows[1]), string(Gone))
		if first != second {
			t.Errorf("reason column at %d and %d, want the same offset", first, second)
		}
	})

	t.Run("no items", func(t *testing.T) {
		if rows := formatRows(nil, "/repo"); len(rows) != 0 {
			t.Errorf("formatRows(nil) = %q, want empty", rows)
		}
	})
}

func TestRed(t *testing.T) {
	// Set either way rather than read, so the developer's own NO_COLOR does not
	// decide which of these passes.
	t.Run("the warning carries the colour", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		if got := red("only here"); got != "\033[31monly here\033[0m" {
			t.Errorf("red = %q", got)
		}
	})

	t.Run("NO_COLOR leaves the text bare", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if got := red("only here"); got != "only here" {
			t.Errorf("red = %q, want the bare text", got)
		}
	})
}

func TestParseSelection(t *testing.T) {
	t.Run("selection round trips through the rows", func(t *testing.T) {
		items := sampleItems()
		got := parseSelection(strings.Join(formatRows(items, ""), "\n"))
		want := []string{items[0].Token(), items[1].Token()}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseSelection = %q, want %q", got, want)
		}
	})

	t.Run("blank lines are ignored", func(t *testing.T) {
		if got := parseSelection("\n\n"); len(got) != 0 {
			t.Errorf("parseSelection = %q, want empty", got)
		}
	})
}

// TestSelectWithFzf drives the fzf path with a stub binary, so no fzf install
// is needed.
func TestSelectWithFzf(t *testing.T) {
	fakeFzf := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fzf")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	rows := []string{"branch:one" + field + "one row", "branch:two" + field + "two row"}

	t.Run("the picked rows come back as tokens", func(t *testing.T) {
		// Echo back the second line of the input, the way fzf would print a
		// selected row verbatim.
		got, ok := selectWithFzf(rows, fakeFzf(t, "sed -n 2p"), "preview", keys)
		if !ok || !reflect.DeepEqual(got, []string{"branch:two"}) {
			t.Errorf("selectWithFzf = %q, %v", got, ok)
		}
	})

	t.Run("cancelling reports no selection", func(t *testing.T) {
		if got, ok := selectWithFzf(rows, fakeFzf(t, "exit 130"), "preview", keys); ok {
			t.Errorf("selectWithFzf = %q, %v, want cancelled", got, ok)
		}
	})

	t.Run("selecting nothing returns no tokens", func(t *testing.T) {
		got, ok := selectWithFzf(rows, fakeFzf(t, "true"), "preview", keys)
		if !ok || len(got) != 0 {
			t.Errorf("selectWithFzf = %q, %v, want an empty selection", got, ok)
		}
	})

	t.Run("the rows are handed to fzf on stdin", func(t *testing.T) {
		got, ok := selectWithFzf(rows[:1], fakeFzf(t, "cat"), "preview", keys)
		if !ok || !reflect.DeepEqual(got, []string{"branch:one"}) {
			t.Errorf("selectWithFzf = %q, %v", got, ok)
		}
	})
}

func TestQuote(t *testing.T) {
	tests := map[string]string{
		"/usr/bin/git-reap":  `'/usr/bin/git-reap'`,
		"/my tools/git-reap": `'/my tools/git-reap'`,
		"/it's/git-reap":     `'/it'\''s/git-reap'`,
	}
	for path, want := range tests {
		if got := quote(path); got != want {
			t.Errorf("quote(%q) = %q, want %q", path, got, want)
		}
	}
}
