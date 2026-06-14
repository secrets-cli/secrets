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
	case strings.HasPrefix(cmd, "log --format=%H"):
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

func TestVersionContent_NoHistory(t *testing.T) {
	f := &fakeGit{logOutput: ""}
	if _, err := repoWith(f).VersionContent("K.age", 1); err == nil {
		t.Fatal("expected no-history error")
	}
}

func TestLog_ParsesLines(t *testing.T) {
	f := &fakeGit{}
	// Override run to return canned log output.
	r := &Repo{dir: "/store", run: func(args ...string) (string, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		return "abc123 set RPC_URL (2026-06-13)\ndef456 mv RPC_URL R2 (2026-06-12)\n", nil
	}}
	lines, err := r.Log("RPC_URL.age")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "set RPC_URL") {
		t.Fatalf("log lines = %v", lines)
	}
	if !f.issued("-- RPC_URL.age") {
		t.Fatalf("log should scope to the file, calls = %v", f.calls)
	}
}
