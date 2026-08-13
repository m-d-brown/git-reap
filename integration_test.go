package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The fixture repository holds one of everything the tool has an opinion about:
// a merged branch, a branch whose upstream was deleted (the squash-merge case),
// an idle branch, an active branch, and worktrees that are merged, dirty,
// idle-detached, and freshly detached. The "remote" is a bare repository next
// door, so the fetch is real but offline.

// ancient is old enough to count as unused at the default threshold.
const ancient = "2020-01-01T00:00:00"

var build struct {
	sync.Once
	path string
	err  error
}

// binary builds git-reap once per run and returns the path to it.
func binary(t *testing.T) string {
	t.Helper()
	build.Do(func() {
		build.path = filepath.Join(os.TempDir(), "git-reap-test-binary")
		output, err := exec.Command("go", "build", "-o", build.path, ".").CombinedOutput()
		if err != nil {
			build.err = err
			t.Logf("go build: %s", output)
		}
	})
	if build.err != nil {
		t.Fatalf("building git-reap: %v", build.err)
	}
	return build.path
}

type repo struct {
	t                  *testing.T
	root, remote, work string
	env                []string
	// pathDir holds the stub fzf, and a git to go with it, so that PATH can be
	// replaced wholesale.
	pathDir string
}

// newRepo builds the fixture repository in a temporary directory.
func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// EvalSymlinks because git reports resolved paths (/tmp is a symlink on
	// macOS) and the assertions below compare against its output.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	r := &repo{
		t:       t,
		root:    root,
		remote:  filepath.Join(root, "remote.git"),
		work:    filepath.Join(root, "work"),
		pathDir: filepath.Join(root, "bin"),
	}
	// Keep the real git config -- signing, hooks, aliases -- out of the
	// fixture, and give commits a fixed identity.
	r.env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
	r.build()
	return r
}

// git runs a fixture git command, failing the test if git does.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	return r.gitIn(r.work, "", args...)
}

// gitIn runs a fixture git command in dir, optionally dating the commit it makes.
func (r *repo) gitIn(dir, when string, args ...string) string {
	r.t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = r.env
	if when != "" {
		command.Env = append(command.Env, "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func (r *repo) path(name string) string { return filepath.Join(r.root, name) }

func (r *repo) commit(name, when string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.work, name), []byte(name), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", name)
	r.gitIn(r.work, when, "commit", "-qm", "add "+name)
}

func (r *repo) mergeIntoMain(branch string) {
	r.t.Helper()
	r.git("checkout", "-qb", branch)
	r.commit(branch+".txt", "")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", branch)
}

func (r *repo) build() {
	r.t.Helper()
	r.gitIn(r.root, "", "init", "-q", "--bare", "-b", "main", r.remote)
	r.gitIn(r.root, "", "clone", "-q", r.remote, r.work)

	r.commit("start.txt", "")
	r.git("push", "-q", "-u", "origin", "main")
	// A fresh clone of an empty repository has no origin/HEAD; set it the way
	// a normal clone would have.
	r.git("remote", "set-head", "origin", "-a")

	r.mergeIntoMain("merged-feature")

	// Squash-merge stand-in: pushed, then deleted on the remote, which leaves
	// the local branch tracking a gone upstream and unmerged.
	r.git("checkout", "-qb", "gone-feature")
	r.commit("gone.txt", "")
	r.git("push", "-q", "-u", "origin", "gone-feature")
	r.git("checkout", "-q", "main")
	r.git("push", "-q", "origin", "--delete", "gone-feature")

	// Unmerged and untouched for years: the "unused" case.
	r.git("checkout", "-qb", "forgotten")
	r.commit("forgotten.txt", ancient)
	r.git("checkout", "-q", "main")

	r.git("checkout", "-qb", "active")
	r.commit("active.txt", "")
	r.git("checkout", "-q", "main")

	r.mergeIntoMain("wt-merged")
	r.git("worktree", "add", "-q", r.path("wt-merged"), "wt-merged")

	r.mergeIntoMain("wt-dirty")
	r.git("worktree", "add", "-q", r.path("wt-dirty"), "wt-dirty")
	if err := os.WriteFile(filepath.Join(r.path("wt-dirty"), "scratch.txt"), []byte("uncommitted"), 0o644); err != nil {
		r.t.Fatal(err)
	}

	// Two detached worktrees, one parked on an ancient commit and one on a
	// current commit -- the agent-worktree shape.
	r.git("worktree", "add", "-q", "--detach", r.path("wt-old"), "forgotten")
	r.git("worktree", "add", "-q", "--detach", r.path("wt-new"))

	// Everything merged above has to reach the remote, since the base is
	// origin/main rather than the local main.
	r.git("push", "-q", "origin", "main")
}

// stubPath builds a PATH holding only git and a stub fzf that picks the rows
// matching a shell glob -- or, given an empty pattern, no fzf at all.
func (r *repo) stubPath(pattern string) string {
	r.t.Helper()
	if err := os.MkdirAll(r.pathDir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		r.t.Fatal(err)
	}
	link := filepath.Join(r.pathDir, "git")
	if _, err := os.Lstat(link); err != nil {
		if err := os.Symlink(git, link); err != nil {
			r.t.Fatal(err)
		}
	}
	fzf := filepath.Join(r.pathDir, "fzf")
	if pattern == "" {
		os.Remove(fzf)
		return r.pathDir
	}
	// Shell built-ins only. The read is guarded so that the last row, which has
	// no newline after it, is still seen, and the exit is explicit because the
	// loop ends on the read that failed at end of input.
	stub := "#!/bin/sh\nwhile IFS= read -r row || [ -n \"$row\" ]; do\n" +
		"  case \"$row\" in " + pattern + ") printf '%s\\n' \"$row\";; esac\ndone\nexit 0\n"
	if err := os.WriteFile(fzf, []byte(stub), 0o755); err != nil {
		r.t.Fatal(err)
	}
	return r.pathDir
}

type result struct {
	stdout, stderr string
	code           int
}

// run invokes git-reap in the fixture repository.
func (r *repo) run(args ...string) result {
	return r.runWith("", "", args...)
}

// runWith invokes git-reap with something on stdin, and optionally with PATH
// replaced by pathDir so a stub fzf (or no fzf at all) is what it finds.
func (r *repo) runWith(stdin, path string, args ...string) result {
	r.t.Helper()
	command := exec.Command(binary(r.t), args...)
	command.Dir = r.work
	command.Env = r.env
	if path != "" {
		command.Env = append(command.Env, "PATH="+path)
	}
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	code := 0
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			r.t.Fatalf("running git-reap: %v", err)
		}
		code = exit.ExitCode()
	}
	return result{stdout.String(), stderr.String(), code}
}

func (r *repo) branches() []string {
	return strings.Fields(r.git("for-each-ref", "--format=%(refname:short)", "refs/heads"))
}

func (r *repo) worktrees() []string {
	var paths []string
	for _, line := range strings.Split(r.git("worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := set(got...)
	for _, name := range want {
		if !set[name] {
			return false
		}
	}
	return true
}

func contains(t *testing.T, output string, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if !strings.Contains(output, want) {
			t.Errorf("output does not mention %q:\n%s", want, output)
		}
	}
}

func lacks(t *testing.T, output string, unwanted ...string) {
	t.Helper()
	for _, unwant := range unwanted {
		if strings.Contains(output, unwant) {
			t.Errorf("output mentions %q, and should not:\n%s", unwant, output)
		}
	}
}

func TestDryRunListsEveryKindOfCandidateAndChangesNothing(t *testing.T) {
	r := newRepo(t)
	branchesBefore, worktreesBefore := r.branches(), r.worktrees()

	got := r.run("-n")
	contains(t, got.stdout, "merged-feature", "upstream gone", "unused", "detached",
		// Kept, with a reason, rather than offered.
		"kept    worktree "+r.path("wt-dirty")+" (uncommitted changes)",
		"detached but recent")
	lacks(t, got.stdout, "active")

	if !equal(r.branches(), branchesBefore) || !equal(r.worktrees(), worktreesBefore) {
		t.Errorf("--dry-run changed the repository: %v %v", r.branches(), r.worktrees())
	}
}

func TestDryRunRowsCarryTheMetadata(t *testing.T) {
	r := newRepo(t)
	for _, line := range strings.Split(r.run("-n").stdout, "\n") {
		if strings.HasPrefix(line, "branch") && strings.Contains(line, "forgotten") {
			contains(t, line, "unused", "ago", "add forgotten.txt")
			return
		}
	}
	t.Error("no row for the forgotten branch")
}

func TestAllDeletesEveryCandidate(t *testing.T) {
	r := newRepo(t)
	r.run("-a", "-y")

	// main is the base; active is recent and unmerged; wt-dirty is pinned by
	// the worktree holding uncommitted changes.
	if want := []string{"main", "active", "wt-dirty"}; !equal(r.branches(), want) {
		t.Errorf("branches = %v, want %v", r.branches(), want)
	}
	want := []string{r.work, r.path("wt-dirty"), r.path("wt-new")}
	if !equal(r.worktrees(), want) {
		t.Errorf("worktrees = %v, want %v", r.worktrees(), want)
	}
	for _, gone := range []string{r.path("wt-merged"), r.path("wt-old")} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s still exists", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(r.path("wt-dirty"), "scratch.txt")); err != nil {
		t.Errorf("the uncommitted file was lost: %v", err)
	}
}

func TestDecliningTheConfirmationDeletesNothing(t *testing.T) {
	r := newRepo(t)
	branchesBefore, worktreesBefore := r.branches(), r.worktrees()

	got := r.runWith("n\n", "", "-a")
	contains(t, got.stdout, "Delete ", "cancelled")
	if !equal(r.branches(), branchesBefore) || !equal(r.worktrees(), worktreesBefore) {
		t.Error("declining the confirmation still changed the repository")
	}
}

func TestDaysControlsWhatCountsAsUnused(t *testing.T) {
	r := newRepo(t)

	// With a one-day threshold everything idle qualifies, including the freshly
	// detached worktree.
	contains(t, r.run("-n", "-d", "1").stdout, filepath.Base(r.path("wt-new")))

	// With a very long threshold nothing is unused, so only merged and gone
	// branches remain candidates. "detached at <sha>" is the detail of a
	// detached candidate; the kept list still mentions detached worktrees, and
	// should.
	got := r.run("-n", "-d", "100000", "--no-fetch")
	lacks(t, got.stdout, "unused", "detached at")
	contains(t, got.stdout, "detached but recent")
}

func TestAWorktreePinsItsBranchWhenOnlyTheBranchIsSelected(t *testing.T) {
	r := newRepo(t)
	// Selecting the branch but not the worktree holding it: git would refuse,
	// so the branch is reported as kept and survives.
	path := r.stubPath("branch:wt-merged*")

	got := r.runWith("", path)
	contains(t, got.stdout, "kept branch wt-merged (checked out at "+r.path("wt-merged")+")")
	if !set(r.branches()...)["wt-merged"] {
		t.Error("wt-merged was deleted out from under its worktree")
	}
}

func TestSelectingAWorktreeAndItsBranchRemovesBoth(t *testing.T) {
	r := newRepo(t)
	path := r.stubPath("*wt-merged*")

	r.runWith("", path)
	if set(r.branches()...)["wt-merged"] {
		t.Error("the wt-merged branch survived")
	}
	if _, err := os.Stat(r.path("wt-merged")); err == nil {
		t.Error("the wt-merged worktree survived")
	}
}

func TestSelectingNothingDeletesNothing(t *testing.T) {
	r := newRepo(t)
	branchesBefore := r.branches()
	path := r.stubPath("*no-such-candidate*")

	got := r.runWith("", path)
	contains(t, got.stdout, "nothing selected")
	if !equal(r.branches(), branchesBefore) {
		t.Error("an empty selection still deleted something")
	}
}

func TestWithoutFzfTheInteractiveRunExplainsItself(t *testing.T) {
	r := newRepo(t)
	got := r.runWith("", r.stubPath(""))
	if got.code != 1 {
		t.Errorf("exit code = %d, want 1", got.code)
	}
	contains(t, got.stderr, "fzf not found")
}

func TestPreviewOfABranchShowsItsHistoryAndWorktree(t *testing.T) {
	r := newRepo(t)
	got := r.run("--preview", "branch:wt-merged", "--no-fetch")
	contains(t, got.stdout, "add wt-merged.txt", "checked out at "+r.path("wt-merged"))
}

func TestPreviewOfAWorktreeShowsItsHeadAndStatus(t *testing.T) {
	r := newRepo(t)
	got := r.run("--preview", "worktree:"+r.path("wt-dirty"), "--no-fetch")
	contains(t, got.stdout, "HEAD ", "scratch.txt")
}

func TestABaseBranchGivenOnTheCommandLineIsUsed(t *testing.T) {
	r := newRepo(t)
	// active was branched after merged-feature landed but before the wt-*
	// merges, so those are no longer merged from its point of view.
	got := r.run("-n", "--no-fetch", "active")
	contains(t, got.stdout, "merged-feature", "wt-merged still in use")
}

func TestNothingToDoIsReported(t *testing.T) {
	r := newRepo(t)
	// Remove the one thing that pins a candidate, so a full pass really does
	// leave nothing behind.
	if err := os.Remove(filepath.Join(r.path("wt-dirty"), "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	r.run("-a", "-y")

	contains(t, r.run("-a", "-y", "--no-fetch").stdout, "nothing to reap")
}

func TestRunningOutsideARepositoryFails(t *testing.T) {
	r := newRepo(t)

	// r.root holds the fixture but is not itself a repository.
	command := exec.Command(binary(t))
	command.Dir = r.root
	command.Env = append(r.env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(r.root))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("git-reap succeeded outside a repository:\n%s", output)
	}
	contains(t, string(output), "not a git repository")
}
