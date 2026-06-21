package git

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vars-cli/vars/internal/vault"
)

// Compile-time contract: a *Repo is a valid vault.Committer.
var _ vault.Committer = (*Repo)(nil)

// fakeGit is a controllable git double. It records the commands issued and
// answers the few queries the Repo logic branches on, so the orchestration can
// be tested deterministically without a real repository.
type fakeGit struct {
	calls       []string // each call as "arg arg arg", in order
	staged      bool     // true => `diff --cached --quiet` reports staged changes (exit 1)
	upstream    bool     // true => @{u} resolves
	configEmail string   // output of `config user.email`
	remotes     string   // output of `remote`
	logOutput   string   // output of `log --format=%H -- <path>`
	showOutput  string   // output of `show <commit>:<path>`
	failOn      string   // if a command contains this substring, return an error
}

func (f *fakeGit) run(args ...string) (string, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return "boom", errors.New("git failed")
	}
	switch {
	case cmd == "diff --cached --quiet":
		if f.staged {
			return "", errors.New("exit status 1")
		}
		return "", nil
	case strings.Contains(cmd, "@{u}"):
		if f.upstream {
			return "origin/main", nil
		}
		return "", errors.New("no upstream")
	case cmd == "symbolic-ref --short HEAD":
		return "main\n", nil
	case cmd == "remote":
		return f.remotes, nil
	case cmd == "config user.email":
		return f.configEmail, nil
	case strings.Contains(cmd, "--format=%H"):
		return f.logOutput, nil
	case strings.HasPrefix(cmd, "show "):
		return f.showOutput, nil
	}
	return "", nil
}

func repoWith(f *fakeGit) *Repo { return &Repo{dir: "/store", run: f.run} }

func (f *fakeGit) issued(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestCommit_SkipsWhenNothingStaged(t *testing.T) {
	f := &fakeGit{staged: false}
	if err := repoWith(f).Commit("set RPC_URL"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	want := []string{"add -A", "diff --cached --quiet"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v (no commit when nothing staged)", f.calls, want)
	}
}

func TestCommit_CommitsWhenStaged(t *testing.T) {
	f := &fakeGit{staged: true}
	if err := repoWith(f).Commit("set RPC_URL"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !f.issued("commit -q -m set RPC_URL") {
		t.Fatalf("expected a commit with the message, calls = %v", f.calls)
	}
}

func TestCommit_PropagatesError(t *testing.T) {
	f := &fakeGit{staged: true, failOn: "commit"}
	if err := repoWith(f).Commit("x"); err == nil {
		t.Fatal("commit failure should propagate")
	}
}

func TestSync_FirstPushSetsUpstream(t *testing.T) {
	f := &fakeGit{remotes: "origin\n", upstream: false}
	if err := repoWith(f).Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !f.issued("push -u origin main") {
		t.Fatalf("first sync should set upstream, calls = %v", f.calls)
	}
	if f.issued("pull") {
		t.Fatalf("first sync should not pull, calls = %v", f.calls)
	}
}

func TestSync_PullThenPushWhenUpstream(t *testing.T) {
	f := &fakeGit{remotes: "origin\n", upstream: true}
	if err := repoWith(f).Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !f.issued("pull --rebase") || !f.issued("push") {
		t.Fatalf("sync with upstream should pull then push, calls = %v", f.calls)
	}
	// push -u must NOT be used when an upstream already exists.
	if f.issued("push -u") {
		t.Fatalf("should not re-set upstream, calls = %v", f.calls)
	}
}

func TestSync_NoRemoteErrors(t *testing.T) {
	f := &fakeGit{remotes: ""}
	if err := repoWith(f).Sync(); err == nil {
		t.Fatal("sync without a remote should error")
	}
	if f.issued("push") || f.issued("pull") {
		t.Fatalf("must not push/pull without a remote, calls = %v", f.calls)
	}
}

func TestSync_AbortsRebaseOnPullFailure(t *testing.T) {
	// A diverged/conflicting remote makes pull --rebase fail; Sync must abort the
	// rebase (so the repo isn't left wedged) and not push.
	f := &fakeGit{remotes: "origin\n", upstream: true, failOn: "pull"}
	if err := repoWith(f).Sync(); err == nil {
		t.Fatal("sync should fail when the rebase can't apply")
	}
	if !f.issued("rebase --abort") {
		t.Fatalf("a failed pull --rebase must be aborted, calls = %v", f.calls)
	}
	if f.issued("push") {
		t.Fatalf("must not push after a failed rebase, calls = %v", f.calls)
	}
}

func TestSync_CommitsPendingBeforePulling(t *testing.T) {
	// A dirty tree (e.g. a prior best-effort auto-commit failed) is committed
	// before the rebase, so sync self-heals rather than failing on a dirty tree.
	f := &fakeGit{remotes: "origin\n", upstream: true, staged: true}
	if err := repoWith(f).Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !f.issued("commit -q -m vars: commit pending changes before sync") {
		t.Fatalf("expected pending changes committed before pull, calls = %v", f.calls)
	}
}

func TestInit_SetsIdentityWhenMissing(t *testing.T) {
	f := &fakeGit{configEmail: ""}
	if err := repoWith(f).Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !f.issued("init -q") {
		t.Fatalf("expected git init, calls = %v", f.calls)
	}
	if !f.issued("config user.email vars@localhost") || !f.issued("config user.name vars") {
		t.Fatalf("expected default identity to be set, calls = %v", f.calls)
	}
}

func TestInit_KeepsExistingIdentity(t *testing.T) {
	f := &fakeGit{configEmail: "me@example.com\n"}
	if err := repoWith(f).Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if f.issued("config user.email vars@localhost") {
		t.Fatalf("must not override an existing identity, calls = %v", f.calls)
	}
}

func TestHasRemote(t *testing.T) {
	if !repoWith(&fakeGit{remotes: "origin\n"}).HasRemote() {
		t.Fatal("expected HasRemote true")
	}
	if repoWith(&fakeGit{remotes: ""}).HasRemote() {
		t.Fatal("expected HasRemote false")
	}
}

func TestVersionContent_SelectsNthCommit(t *testing.T) {
	f := &fakeGit{logOutput: "h0\nh1\nh2\n", showOutput: "CIPHERTEXT"}
	got, err := repoWith(f).VersionContent("RPC_URL.age", 2)
	if err != nil {
		t.Fatalf("VersionContent: %v", err)
	}
	if string(got) != "CIPHERTEXT" {
		t.Fatalf("got %q", got)
	}
	if !f.issued("show h2:RPC_URL.age") {
		t.Fatalf("expected show of h2, calls=%v", f.calls)
	}
}

func TestVersionContent_OutOfBounds(t *testing.T) {
	f := &fakeGit{logOutput: "h0\nh1\n"} // current + 1 previous
	if _, err := repoWith(f).VersionContent("K.age", 5); err == nil {
		t.Fatal("expected out-of-bounds error")
	}
}

func TestVersionContent_RemovalHasNoValue(t *testing.T) {
	// commits[0] is the commit that removed the key: cat-file -e fails, so the
	// version reports "no value" rather than leaking a raw git show failure.
	f := &fakeGit{logOutput: "h0\nh1\n", failOn: "cat-file"}
	_, err := repoWith(f).VersionContent("K.age", 0)
	if err == nil || !strings.Contains(err.Error(), "no value") {
		t.Fatalf("expected a clear no-value error, got %v", err)
	}
}

func TestVersionContent_NoHistory(t *testing.T) {
	f := &fakeGit{logOutput: ""}
	if _, err := repoWith(f).VersionContent("K.age", 1); err == nil {
		t.Fatal("expected no-history error")
	}
}

func TestLog_RendersValuesAndRemovals(t *testing.T) {
	var calls []string
	// Full history h0..h2 where h1 was a removal (absent from the AMR value set).
	r := &Repo{dir: "/store", run: func(args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		switch {
		case strings.Contains(cmd, "--diff-filter=AMR"):
			return "h0\nh2\n", nil // value-bearing commits only
		case strings.Contains(cmd, "--format=%H"):
			return "h0\x1f2026-06-21 09:10\x1fset RPC_URL\n" +
				"h1\x1f2026-06-21 09:05\x1frm RPC_URL\n" +
				"h2\x1f2026-06-21 09:01\x1fset RPC_URL\n", nil
		}
		return "", nil
	}}
	lines, err := r.Log("RPC_URL.age")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("want 3 state lines, got %v", lines)
	}
	if !strings.Contains(lines[0], "set RPC_URL") {
		t.Fatalf("line 0 should be a value: %q", lines[0])
	}
	if !strings.Contains(lines[1], "(removed)") || strings.Contains(lines[1], "rm RPC_URL") {
		t.Fatalf("line 1 should render the removal as a no-value state, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "set RPC_URL") {
		t.Fatalf("line 2 should be a value: %q", lines[2])
	}
	scoped := false
	for _, c := range calls {
		if strings.Contains(c, "-- RPC_URL.age") {
			scoped = true
		}
	}
	if !scoped {
		t.Fatalf("log should scope to the file, calls = %v", calls)
	}
}

// TestGitExec_ReturnsSubprocessOutput exercises the real runner (the fake
// bypasses it). It guards a bug we shipped: `return buf.String(), cmd.Run()`
// reads the buffer before Run() executes, since Go evaluates return operands
// left to right, so every output-reading call (Log, HasRemote, …) saw "".
// `git version` needs no repository, so it's hermetic.
func TestGitExec_ReturnsSubprocessOutput(t *testing.T) {
	if !Available() {
		t.Skip("git not installed")
	}
	out, err := gitExec(t.TempDir(), "version")
	if err != nil {
		t.Fatalf("git version: %v", err)
	}
	if !strings.Contains(out, "git version") {
		t.Fatalf("gitExec returned no subprocess output: %q", out)
	}
}
