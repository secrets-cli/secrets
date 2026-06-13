package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vars-cli/vars/internal/vault"
)

// Compile-time contract: a *Repo is a valid vault.Committer.
var _ vault.Committer = (*Repo)(nil)

// requireWorkingGit skips when git is absent, or when the environment isolates
// each git subprocess's filesystem view — some sandboxes do, which makes any
// multi-invocation git workflow untestable (and is purely an environment trait,
// not a code issue). Probe: a repo created by one git process must be visible
// to the next. These tests run fully on a real filesystem (CI, dev machines).
func requireWorkingGit(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if _, err := run(dir, "init", "-q"); err != nil {
		t.Skipf("git init failed: %v", err)
	}
	out, err := run(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		t.Skip("environment isolates nested git subprocesses; run git tests on a real filesystem")
	}
}

func TestRepo_InitAndCommit(t *testing.T) {
	requireWorkingGit(t)
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !IsRepo(dir) {
		t.Fatal("dir should be a git repo after Init")
	}
	r := New(dir)

	// Nothing changed yet → Commit is a no-op, not an error.
	if err := r.Commit("noop"); err != nil {
		t.Fatalf("empty commit should be a no-op: %v", err)
	}

	os.WriteFile(filepath.Join(dir, "RPC_URL.age"), []byte("ciphertext"), 0o600)
	if err := r.Commit("set RPC_URL"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	lines, err := r.Log("RPC_URL.age")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "set RPC_URL") {
		t.Fatalf("log = %v, want one 'set RPC_URL' entry", lines)
	}

	// Committing again with no change must not create a second commit.
	if err := r.Commit("again"); err != nil {
		t.Fatalf("second no-op commit: %v", err)
	}
	if lines, _ := r.Log("RPC_URL.age"); len(lines) != 1 {
		t.Fatalf("expected still 1 commit, got %d", len(lines))
	}
}

func TestRepo_HasRemote(t *testing.T) {
	requireWorkingGit(t)
	dir := t.TempDir()
	Init(dir)
	r := New(dir)
	if r.HasRemote() {
		t.Fatal("fresh repo should have no remote")
	}
	if _, err := run(dir, "remote", "add", "origin", t.TempDir()); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if !r.HasRemote() {
		t.Fatal("expected a remote after adding one")
	}
}

func TestRepo_SyncFirstPushThenUpdate(t *testing.T) {
	requireWorkingGit(t)
	// Bare remote.
	remote := t.TempDir()
	if _, err := run(remote, "init", "--bare", "-q"); err != nil {
		t.Fatalf("bare init: %v", err)
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := New(dir)

	// Sync with no remote should explain itself.
	if err := r.Sync(); err == nil {
		t.Fatal("Sync without a remote should error")
	}

	os.WriteFile(filepath.Join(dir, "a.age"), []byte("x"), 0o600)
	r.Commit("set a")
	if _, err := run(dir, "remote", "add", "origin", remote); err != nil {
		t.Fatalf("remote add: %v", err)
	}

	// First sync: no upstream yet → should push and set upstream.
	if err := r.Sync(); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Second commit + sync: upstream now exists → pull --rebase + push.
	os.WriteFile(filepath.Join(dir, "b.age"), []byte("y"), 0o600)
	r.Commit("set b")
	if err := r.Sync(); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// The bare remote should now hold a branch with both commits.
	out, err := run(remote, "log", "--oneline")
	if err != nil {
		t.Fatalf("remote log: %v", err)
	}
	if !strings.Contains(out, "set a") || !strings.Contains(out, "set b") {
		t.Fatalf("remote missing commits:\n%s", out)
	}
}
