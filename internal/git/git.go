// Package git wraps the git CLI for the vars store. The store directory is a
// git repo, so secrets are versioned (git log = history) and syncable
// (push/pull). git is a soft dependency: only versioning/sync need it.
//
// It shells out to the user's own git so their config, credentials, remotes,
// and commit signing all apply — and so `vars git …` can pass through verbatim.
//
// Every git invocation goes through Repo.run, a function field that defaults to
// real exec but is replaced in tests, so the orchestration logic is unit-tested
// without depending on a real repository.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Repo is a git working tree rooted at dir.
type Repo struct {
	dir string
	run func(args ...string) (string, error) // git -C dir <args>; combined output
}

// New returns a Repo handle for dir that shells out to the real git binary.
func New(dir string) *Repo {
	return &Repo{dir: dir, run: func(args ...string) (string, error) {
		return gitExec(dir, args...)
	}}
}

// Available reports whether the git binary is installed.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run() == nil
}

// Init initializes a git repo in dir (which must already exist) and ensures a
// commit identity, so auto-commits never fail for lack of one. An existing
// global/local identity is left untouched.
func (r *Repo) Init() error {
	if out, err := r.run("init", "-q"); err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	if cur, _ := r.run("config", "user.email"); strings.TrimSpace(cur) == "" {
		r.run("config", "user.email", "vars@localhost")
		r.run("config", "user.name", "vars")
	}
	return nil
}

// Commit stages all changes and commits. "Nothing to commit" is not an error,
// so callers can commit unconditionally after a mutation.
func (r *Repo) Commit(message string) error {
	if out, err := r.run("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w: %s", err, out)
	}
	if r.nothingStaged() {
		return nil
	}
	if out, err := r.run("commit", "-q", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, out)
	}
	return nil
}

// HasRemote reports whether at least one remote is configured.
func (r *Repo) HasRemote() bool {
	out, err := r.run("remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// Sync pulls (rebase) then pushes. On the first push (no upstream yet) it sets
// the upstream instead of failing.
func (r *Repo) Sync() error {
	if !r.HasRemote() {
		return errors.New("no git remote configured; add one with `vars git remote add origin <url>`")
	}
	if r.hasUpstream() {
		if out, err := r.run("pull", "--rebase"); err != nil {
			return fmt.Errorf("git pull --rebase: %w: %s", err, out)
		}
		if out, err := r.run("push"); err != nil {
			return fmt.Errorf("git push: %w: %s", err, out)
		}
		return nil
	}
	branch, err := r.currentBranch()
	if err != nil {
		return err
	}
	if out, err := r.run("push", "-u", r.firstRemote(), branch); err != nil {
		return fmt.Errorf("git push: %w: %s", err, out)
	}
	return nil
}

// Log returns commit lines (newest first) touching relpath, formatted
// "<short-hash> <subject> (<date>)". Empty when relpath has no history.
func (r *Repo) Log(relpath string) ([]string, error) {
	out, err := r.run("log", "--format=%h %s (%cs)", "--", relpath)
	if err != nil {
		return nil, fmt.Errorf("git log: %w: %s", err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// VersionContent returns the raw stored bytes of relpath as of n commits back
// that touched it (n=0 = current, n=1 = previous, …), via git. The caller
// decrypts. .age files are binary, so git emits them unmodified.
func (r *Repo) VersionContent(relpath string, n int) ([]byte, error) {
	logOut, err := r.run("log", "--format=%H", "--", relpath)
	if err != nil {
		return nil, fmt.Errorf("git log: %w: %s", err, logOut)
	}
	commits := strings.Fields(strings.TrimSpace(logOut))
	if len(commits) == 0 {
		return nil, fmt.Errorf("%q has no history", relpath)
	}
	if n < 0 || n >= len(commits) {
		return nil, fmt.Errorf("only %d previous version(s) exist", len(commits)-1)
	}
	out, err := r.run("show", commits[n]+":"+relpath)
	if err != nil {
		return nil, fmt.Errorf("git show: %w: %s", err, out)
	}
	return []byte(out), nil
}

// Passthrough runs `git <args>` in the store dir with the process's own stdio,
// backing `vars git …`. It returns the command error (callers may inspect exit code).
func (r *Repo) Passthrough(args []string) error {
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (r *Repo) nothingStaged() bool {
	// `diff --cached --quiet` exits 0 (nil err) when nothing is staged.
	_, err := r.run("diff", "--cached", "--quiet")
	return err == nil
}

func (r *Repo) currentBranch() (string, error) {
	out, err := r.run("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determining current branch: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

func (r *Repo) hasUpstream() bool {
	_, err := r.run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return err == nil
}

func (r *Repo) firstRemote() string {
	out, _ := r.run("remote")
	if fields := strings.Fields(out); len(fields) > 0 {
		return fields[0]
	}
	return "origin"
}

// gitExec runs a git subcommand in dir and returns combined output.
func gitExec(dir string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run() // must run before reading buf: `return buf.String(), cmd.Run()`
	return buf.String(), err
}
