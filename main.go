// Command git-reap deletes branches that are done with, and the worktrees
// sitting on them.
//
// Four things qualify for deletion:
//
//	merged         a branch already contained in the base branch
//	upstream gone  a branch whose remote branch was deleted -- what a
//	               squash-merged and closed pull request leaves behind
//	unused         a branch with no commits in the last --days days
//	detached       a clean worktree on a detached HEAD, idle for --days days,
//	               which is what agent tooling under .claude/worktrees leaves
//
// Worktrees are removed before branches, because git refuses to delete a branch
// that a worktree has checked out. A worktree that is locked, dirty, or the one
// you are standing in is never offered.
//
// By default the candidates go through fzf so you can pick: each row carries
// the age, the tracking state, and the last commit subject, and the preview
// pane shows the recent history. Without fzf, use --dry-run to look and --all
// to take everything.
//
// Installed as git-reap on PATH, so this also runs as `git reap`.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	secondsPerDay = 86400
	defaultDays   = 90
)

// usage is written out by hand: the flag package lists every alias on its own
// line, which reads badly when each option has both a short and a long name.
const usage = `usage: git reap [options] [base]

Delete merged, gone, and unused branches and their worktrees.

  base            branch to measure 'merged' against (default: origin/HEAD,
                  falling back to origin/main, origin/master, main, master)

options:
  -n, --dry-run   list the candidates, and what was skipped, without deleting
  -a, --all       take every candidate instead of picking through fzf
  -y, --yes       skip the confirmation prompt that --all asks for
  -d, --days N    how quiet a branch or detached worktree must be to count as
                  unused (default: 90)
      --no-fetch  skip the 'git fetch --prune' that refreshes remote state
  -h, --help      show this message
`

type options struct {
	base    string
	dryRun  bool
	all     bool
	yes     bool
	days    int
	fetch   bool
	preview string
}

func parseArgs(argv []string) (options, error) {
	opts := options{}
	flags := flag.NewFlagSet("git-reap", flag.ContinueOnError)
	noFetch := false

	// Each option is registered twice, once short and once long, which is how
	// the flag package spells what getopt would call a short alias.
	for _, name := range []string{"n", "dry-run"} {
		flags.BoolVar(&opts.dryRun, name, false, "list the candidates, and what was skipped, without deleting")
	}
	for _, name := range []string{"a", "all"} {
		flags.BoolVar(&opts.all, name, false, "take every candidate instead of picking through fzf")
	}
	for _, name := range []string{"y", "yes"} {
		flags.BoolVar(&opts.yes, name, false, "skip the confirmation prompt that --all asks for")
	}
	for _, name := range []string{"d", "days"} {
		flags.IntVar(&opts.days, name, defaultDays,
			"how quiet a branch or detached worktree must be to count as unused")
	}
	flags.BoolVar(&noFetch, "no-fetch", false, "skip the 'git fetch --prune' that refreshes remote state")
	// Used by fzf's preview pane, not by hand.
	flags.StringVar(&opts.preview, "preview", "", "")

	flags.Usage = func() { fmt.Fprint(flags.Output(), usage) }

	// flag stops at the first positional argument, so parse what is left after
	// it repeatedly to accept options on either side of the base.
	if err := flags.Parse(argv); err != nil {
		return opts, err
	}
	for flags.NArg() > 0 {
		if opts.base != "" {
			return opts, fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		opts.base = flags.Arg(0)
		if err := flags.Parse(flags.Args()[1:]); err != nil {
			return opts, err
		}
	}

	opts.fetch = !noFetch
	return opts, nil
}

func main() {
	os.Exit(reap(os.Args[1:]))
}

func reap(argv []string) int {
	opts, err := parseArgs(argv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if gitTry("rev-parse", "--git-dir") == "" {
		return fail(errors.New("not a git repository"))
	}
	if opts.preview != "" {
		fmt.Println(renderPreview(opts.preview))
		return 0
	}

	// The fetch is what makes "[gone]" accurate: until the remote refs are
	// pruned, a branch whose upstream was deleted still looks alive.
	if opts.fetch {
		gitRun("fetch", "--prune", "--quiet")
	}

	base := findBase(opts.base)
	if base == "" {
		return fail(errors.New("could not work out a base branch; pass one"))
	}

	// Worktrees whose directory was deleted by hand leave admin files behind in
	// .git/worktrees; clearing those is safe enough to do unprompted.
	if opts.dryRun {
		gitRun("worktree", "prune", "--dry-run", "--verbose")
	} else {
		gitRun("worktree", "prune")
	}

	porcelain, err := gitCapture("worktree", "list", "--porcelain")
	if err != nil {
		return fail(err)
	}
	worktrees := parseWorktrees(porcelain)
	states := gatherStates(worktrees)

	mergedOutput, err := gitCapture("branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return fail(err)
	}
	merged := map[string]bool{}
	for _, name := range lines(mergedOutput) {
		merged[name] = true
	}

	// Never candidates: the base, whatever HEAD is on, and the branch the main
	// worktree holds, which no amount of worktree removal can free.
	protected := map[string]bool{strings.TrimPrefix(base, "origin/"): true}
	if current := gitTry("symbolic-ref", "--quiet", "--short", "HEAD"); current != "" {
		protected[current] = true
	}
	if len(worktrees) > 0 && worktrees[0].Branch != "" {
		protected[worktrees[0].Branch] = true
	}

	refs, err := gitCapture("for-each-ref", "--format="+branchFormat, "refs/heads")
	if err != nil {
		return fail(err)
	}
	staleBefore := time.Now().Unix() - int64(opts.days)*secondsPerDay
	branches := parseBranches(refs)
	candidates := classifyBranches(branches, merged, protected, staleBefore)

	root, err := gitCapture("rev-parse", "--show-toplevel")
	if err != nil {
		return fail(err)
	}
	worktreeItems, kept := planWorktrees(worktrees, states, candidates, root, staleBefore)
	// Worktrees first: git refuses to delete a branch one has checked out.
	items := append(worktreeItems, branchItems(branches, candidates)...)

	if len(items) == 0 {
		fmt.Println("git-reap: nothing to reap")
		return 0
	}

	rows := formatRows(items, root)
	if opts.dryRun {
		// Only the display half; the token exists for fzf, not for reading.
		for _, row := range rows {
			fmt.Println(display(row))
		}
		for _, keep := range kept {
			fmt.Printf("kept    worktree %s (%s)\n", keep.Path, keep.Reason)
		}
		return 0
	}

	selected := map[string]bool{}
	if opts.all {
		for _, item := range items {
			selected[item.Token()] = true
		}
		if !opts.yes {
			for _, row := range rows {
				fmt.Println(display(row))
			}
			if !confirm(len(items)) {
				fmt.Println("git-reap: cancelled")
				return 0
			}
		}
	} else {
		fzf, err := exec.LookPath("fzf")
		if err != nil {
			return fail(errors.New("fzf not found; use --dry-run to look or --all to " +
				"take everything (brew install fzf)"))
		}
		self, err := os.Executable()
		if err != nil {
			return fail(err)
		}
		picked, ok := selectWithFzf(rows, fzf, quote(self)+" --preview")
		if !ok || len(picked) == 0 {
			fmt.Println("git-reap: nothing selected")
			return 0
		}
		for _, token := range picked {
			selected[token] = true
		}
	}

	execute(items, selected, worktrees)
	return 0
}

func confirm(count int) bool {
	fmt.Printf("Delete %d item(s)? [y/N] ", count)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && answer == "" {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// quote wraps a path for the shell that fzf runs the preview command through.
func quote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "git-reap: %v\n", err)
	return 1
}
