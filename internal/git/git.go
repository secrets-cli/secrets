// Package git wraps the git CLI for the vars store. The store directory is a
// git repo, so secrets are versioned (git log = history) and syncable
// (push/pull). git is a soft dependency: only versioning/sync need it.
//
// It shells out to the user's own git so their config, credentials, remotes,
// and commit signing all apply — and so `vars git …` can pass through verbatim.
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
type Repo struct{ dir string }

// New returns a Repo handle for dir. It does not verify dir is a repo.
func New(dir string) *Repo { return &Repo{dir: dir} }

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
func Init(dir string) error {
	if out, err := run(dir, "init", "-q"); err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	if cur, _ := run(dir, "config", "user.email"); strings.TrimSpace(cur) == "" {
		run(dir, "config", "user.email", "vars@localhost")
		run(dir, "config", "user.name", "vars")
	}
	return nil
}

// Commit stages all changes and commits. "Nothing to commit" is not an error,
// so callers can commit unconditionally after a mutation.
func (r *Repo) Commit(message string) error {
	if out, err := run(r.dir, "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w: %s", err, out)
	}
	if r.nothingStaged() {
		return nil
	}
	if out, err := run(r.dir, "commit", "-q", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, out)
	}
	return nil
}

// HasRemote reports whether at least one remote is configured.
func (r *Repo) HasRemote() bool {
	out, err := run(r.dir, "remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// Sync pulls (rebase) then pushes. On the first push (no upstream yet) it sets
// the upstream instead of failing.
func (r *Repo) Sync() error {
	if !r.HasRemote() {
		return errors.New("no git remote configured; add one with `vars git remote add origin <url>`")
	}
	if r.hasUpstream() {
		if out, err := run(r.dir, "pull", "--rebase"); err != nil {
			return fmt.Errorf("git pull --rebase: %w: %s", err, out)
		}
		if out, err := run(r.dir, "push"); err != nil {
			return fmt.Errorf("git push: %w: %s", err, out)
		}
		return nil
	}
	branch, err := r.currentBranch()
	if err != nil {
		return err
	}
	if out, err := run(r.dir, "push", "-u", r.firstRemote(), branch); err != nil {
		return fmt.Errorf("git push: %w: %s", err, out)
	}
	return nil
}

// Log returns commit lines (newest first) touching relpath, formatted
// "<short-hash> <subject> (<date>)". Empty when relpath has no history.
func (r *Repo) Log(relpath string) ([]string, error) {
	out, err := run(r.dir, "log", "--format=%h %s (%cs)", "--", relpath)
	if err != nil {
		return nil, fmt.Errorf("git log: %w: %s", err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Passthrough runs `git <args>` in the store dir with the process's own stdio,
// backing `vars git …`. It returns the command error (callers may inspect exit code).
func (r *Repo) Passthrough(args []string) error {
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (r *Repo) nothingStaged() bool {
	// Exit 0 from `diff --cached --quiet` means no staged changes.
	return exec.Command("git", "-C", r.dir, "diff", "--cached", "--quiet").Run() == nil
}

func (r *Repo) currentBranch() (string, error) {
	out, err := run(r.dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determining current branch: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

func (r *Repo) hasUpstream() bool {
	_, err := run(r.dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return err == nil
}

func (r *Repo) firstRemote() string {
	out, _ := run(r.dir, "remote")
	if fields := strings.Fields(out); len(fields) > 0 {
		return fields[0]
	}
	return "origin"
}

// run executes a git subcommand in dir and returns combined output.
func run(dir string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	return buf.String(), cmd.Run()
}
