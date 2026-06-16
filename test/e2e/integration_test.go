//go:build integration

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "vars-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	binary = filepath.Join(tmp, "vars")
	// Build by module path, not ".", since this test package lives in test/e2e.
	if out, err := exec.Command("go", "build", "-o", binary, "github.com/vars-cli/vars").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building binary: %v\n%s\n", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

type runner struct {
	t        *testing.T
	storeDir string
	workDir  string
	env      []string
}

// newRunner builds an isolated environment: a fresh store dir and a dedicated
// ed25519 key bound via VARS_SSH_KEY (so no ssh-agent is needed). The store is
// created lazily on the first command (first-run).
func newRunner(t *testing.T) *runner {
	t.Helper()
	storeDir := t.TempDir()
	workDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath, "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	return &runner{
		t: t, storeDir: storeDir, workDir: workDir,
		env: []string{
			"VARS_STORE_DIR=" + storeDir,
			"VARS_SSH_KEY=" + keyPath,
			"HOME=" + t.TempDir(),
			"PATH=" + os.Getenv("PATH"),
		},
	}
}

func (r *runner) run(args ...string) (stdout, stderr string, err error) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.workDir
	cmd.Env = r.env
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

func (r *runner) runStdin(stdin string, args ...string) (stdout, stderr string, err error) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.workDir
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader(stdin)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

func (r *runner) mustRun(args ...string) string {
	r.t.Helper()
	so, se, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("vars %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, so, se)
	}
	return so
}

func (r *runner) mustFail(args ...string) (stdout, stderr string) {
	r.t.Helper()
	so, se, err := r.run(args...)
	if err == nil {
		r.t.Fatalf("vars %s should have failed\nstdout: %s\nstderr: %s", strings.Join(args, " "), so, se)
	}
	return so, se
}

func (r *runner) writeFile(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.workDir, name), []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
}

func has(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q in:\n%s", sub, s)
	}
}

// --- store CRUD ---

func TestSetGet(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "https://rpc.example.com")
	if got := r.mustRun("get", "RPC_URL"); got != "https://rpc.example.com" {
		t.Fatalf("get = %q", got)
	}
}

func TestGetMissing(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SEED", "x") // create the store
	r.mustFail("get", "NOPE")
}

func TestScopedKeysAreDirectories(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "prod/PRIVATE_KEY", "0xPROD")
	if _, err := os.Stat(filepath.Join(r.storeDir, "prod", "PRIVATE_KEY.age")); err != nil {
		t.Fatalf("expected prod/PRIVATE_KEY.age: %v", err)
	}
	if got := r.mustRun("get", "prod/PRIVATE_KEY"); got != "0xPROD" {
		t.Fatalf("get = %q", got)
	}
	// The descriptor is store.json, unencrypted.
	if _, err := os.Stat(filepath.Join(r.storeDir, "store.json")); err != nil {
		t.Fatalf("expected store.json: %v", err)
	}
}

func TestLsTreeAndSubtree(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "1")
	r.mustRun("set", "prod/PRIVATE_KEY", "2")
	r.mustRun("set", "prod/RPC_URL", "3")

	tree := r.mustRun("ls")
	has(t, tree, "RPC_URL")
	has(t, tree, "prod/")
	if !strings.Contains(tree, "  PRIVATE_KEY") { // indented under prod/
		t.Fatalf("expected indented leaf under prod/, got:\n%s", tree)
	}

	sub := r.mustRun("ls", "prod")
	has(t, sub, "PRIVATE_KEY")
	has(t, sub, "RPC_URL")
}

func TestScopeLs(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "main/dev/RPC_URL", "1")
	r.mustRun("set", "prod/KEY", "2")
	out := r.mustRun("scope", "ls")
	for _, want := range []string{"main", "main/dev", "prod"} {
		has(t, out, want)
	}
}

func TestSetConflict(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "K", "v1")
	// Non-interactive set of a different value without a flag must fail.
	r.mustFail("set", "K", "v2")
	// --skip leaves it.
	r.mustRun("set", "--skip", "K", "v2")
	if got := r.mustRun("get", "K"); got != "v1" {
		t.Fatalf("--skip changed value: %q", got)
	}
	// --replace overwrites.
	r.mustRun("set", "--replace", "K", "v2")
	if got := r.mustRun("get", "K"); got != "v2" {
		t.Fatalf("--replace failed: %q", got)
	}
}

// --- resolve ---

func TestResolveFormats(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "https://rpc")
	r.writeFile(".vars.yaml", "keys:\n  - RPC_URL\n")

	has(t, r.mustRun("resolve"), `export RPC_URL='https://rpc'`)
	has(t, r.mustRun("resolve", "--dotenv"), "RPC_URL=https://rpc")
	has(t, r.mustRun("resolve", "--fish"), "set -x RPC_URL")
}

func TestResolvePartialAndStdin(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "https://rpc")
	r.writeFile(".vars.yaml", "keys:\n  - RPC_URL\n  - DOTENV_ONLY\n")

	so, _, err := r.runStdin("DOTENV_ONLY=from_dotenv\nPASSTHROUGH=p\n", "resolve", "--partial")
	if err != nil {
		t.Fatalf("resolve --partial: %v", err)
	}
	has(t, so, "https://rpc") // from store
	has(t, so, "from_dotenv") // dotenv fallback for a manifest key
	has(t, so, "PASSTHROUGH") // non-manifest stdin key passes through
}

func TestResolveShellEnvFallback(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SEED", "x")
	r.writeFile(".vars.yaml", "keys:\n  - FROM_SHELL\n")
	// Present in the shell env → no export emitted, but resolve succeeds.
	cmd := exec.Command(binary, "resolve", "--origin")
	cmd.Dir = r.workDir
	cmd.Env = append(append([]string{}, r.env...), "FROM_SHELL=shellval")
	out, _ := cmd.Output()
	has(t, string(out), "# FROM_SHELL  shell")
}

func TestResolveProfilesAndGlobalAndInline(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "dev/PRIVATE_KEY", "0xDEV")
	r.mustRun("set", "prod/PRIVATE_KEY", "0xPROD")
	r.mustRun("set", "ETHERSCAN_API_v2", "abc")
	r.writeFile(".vars.yaml", `keys:
  - PRIVATE_KEY
  - ETHERSCAN_API
  - LOG_LEVEL
profiles:
  global:
    ETHERSCAN_API: ETHERSCAN_API_v2
    LOG_LEVEL: = info
  default:
    PRIVATE_KEY: dev/PRIVATE_KEY
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY
`)
	// default profile auto-applied + global + inline literal.
	def := r.mustRun("resolve")
	has(t, def, "0xDEV")
	has(t, def, "abc")              // global alias
	has(t, def, `LOG_LEVEL='info'`) // inline literal
	// named profile overrides.
	has(t, r.mustRun("resolve", "-p", "mainnet"), "0xPROD")
}

func TestResolveScopeFallbackDeepestFirst(t *testing.T) {
	r := newRunner(t)
	// Only the intermediate level exists; not the full key, not the bare key.
	r.mustRun("set", "main/RPC_URL", "https://main.rpc")
	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  mainnet:
    RPC_URL: main/dev/RPC_URL
`)
	// main/dev/RPC_URL missing -> strip deepest -> main/RPC_URL hits.
	has(t, r.mustRun("resolve", "-p", "mainnet"), "https://main.rpc")
}

func TestResolveMissingKeyErrors(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SEED", "x")
	r.writeFile(".vars.yaml", "keys:\n  - ABSENT\n")
	_, se := r.mustFail("resolve")
	has(t, se, "not found in store")
}

// --- resolve profile hint (front-layer behavior carried over) ---

func TestResolveProfileHint(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "DEPLOYER_KEY", "0xSECRET")
	r.writeFile(".vars.yaml", `keys:
  - DEPLOYMENT_PRIVATE_KEY
profiles:
  common:
    DEPLOYMENT_PRIVATE_KEY: DEPLOYER_KEY
  sepolia:
    DEPLOYMENT_PRIVATE_KEY: dev/DEPLOYER_KEY
`)
	_, se := r.mustFail("resolve")
	has(t, se, "Hint:")
	has(t, se, "DEPLOYER_KEY")
	has(t, se, "vars resolve --profile")
	has(t, se, "Available profiles:")
}

func TestResolveProfileHint_SuppressedWhenProfileActive(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "DEPLOYER_KEY", "0xSECRET")
	r.writeFile(".vars.yaml", `keys:
  - DEPLOYMENT_PRIVATE_KEY
profiles:
  common:
    DEPLOYMENT_PRIVATE_KEY: DEPLOYER_KEY
`)
	has(t, r.mustRun("resolve", "--profile", "common"), "0xSECRET")
}

func TestResolveProfileHint_NoneWhenNoProfiles(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SEED", "x")
	r.writeFile(".vars.yaml", "keys:\n  - MISSING_KEY\n")
	_, se := r.mustFail("resolve")
	if strings.Contains(se, "Hint:") {
		t.Fatalf("unexpected hint: %s", se)
	}
}

// --- import ---

func TestImport(t *testing.T) {
	r := newRunner(t)
	r.writeFile(".env", "A=aaa\nB=bbb\n")
	r.mustRun("import", filepath.Join(r.workDir, ".env"))
	if got := r.mustRun("get", "A"); got != "aaa" {
		t.Fatalf("A = %q", got)
	}

	// Scope prefix.
	r.writeFile("dev.env", "RPC=devrpc\n")
	r.mustRun("import", "dev", filepath.Join(r.workDir, "dev.env"))
	if got := r.mustRun("get", "dev/RPC"); got != "devrpc" {
		t.Fatalf("dev/RPC = %q", got)
	}
}

func TestImportConflict(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "A", "orig")
	r.writeFile(".env", "A=new\nB=bbb\n")

	// --skip keeps existing, imports new.
	r.mustRun("import", "--skip", filepath.Join(r.workDir, ".env"))
	if got := r.mustRun("get", "A"); got != "orig" {
		t.Fatalf("--skip changed A: %q", got)
	}
	if got := r.mustRun("get", "B"); got != "bbb" {
		t.Fatalf("B not imported: %q", got)
	}
	// --replace overwrites.
	r.mustRun("import", "--replace", filepath.Join(r.workDir, ".env"))
	if got := r.mustRun("get", "A"); got != "new" {
		t.Fatalf("--replace failed: %q", got)
	}
}

// --- mv / rm / dump ---

func TestMv(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "OLD", "v")
	r.mustRun("mv", "OLD", "scoped/NEW", "--force")
	if got := r.mustRun("get", "scoped/NEW"); got != "v" {
		t.Fatalf("renamed get = %q", got)
	}
	r.mustFail("get", "OLD")
	// Renaming onto an existing key fails.
	r.mustRun("set", "X", "1")
	r.mustRun("set", "Y", "2")
	r.mustFail("mv", "X", "Y", "--force")
}

func TestRm(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "A", "1")
	r.mustRun("set", "B", "2")
	r.mustRun("set", "C", "3")
	r.mustRun("rm", "A", "--force")
	r.mustFail("get", "A")
	r.mustRun("rm", "B", "C", "--force")
	r.mustFail("get", "B")
	r.mustFail("get", "C")
	// Removing a missing key fails.
	r.mustFail("rm", "GHOST", "--force")
}

func TestDump(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "A", "1")
	r.mustRun("set", "prod/B", "2")
	out := r.mustRun("dump", "--dotenv")
	has(t, out, "A=1")
	has(t, out, "prod/B=2")
}

// --- history (git-backed; skipped where git isn't functional) ---

func TestHistory(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "v1")
	r.mustRun("set", "--replace", "RPC_URL", "v2")
	so, _, err := r.run("history", "RPC_URL")
	if err != nil || strings.TrimSpace(so) == "" {
		t.Skip("git history unavailable in this environment")
	}
	has(t, so, "RPC_URL")
}

func TestSetRejectsTilde(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "OK", "1") // create the store
	r.mustFail("set", "BAD~2", "x")
}

func TestGetVersion(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "RPC_URL", "v1")
	r.mustRun("set", "--replace", "RPC_URL", "v2")
	r.mustRun("set", "--replace", "RPC_URL", "v3")
	if got := r.mustRun("get", "RPC_URL"); got != "v3" {
		t.Fatalf("current = %q", got)
	}
	// ~N reads from git history; skip where git isn't functional.
	so, _, err := r.run("get", "RPC_URL~1")
	if err != nil {
		t.Skip("git history unavailable in this environment")
	}
	if so != "v2" {
		t.Fatalf("RPC_URL~1 = %q, want v2", so)
	}
	if got, _, _ := r.run("get", "RPC_URL~2"); got != "v1" {
		t.Fatalf("RPC_URL~2 = %q, want v1", got)
	}
}

// --- value-handling hardening (2026-06-15 audit) ---

func TestSetStdinMultiline(t *testing.T) {
	r := newRunner(t)
	pem := "-----BEGIN-----\nLINE2\n-----END-----" // no trailing newline
	if _, _, err := r.runStdin(pem, "set", "TLS_KEY", "-"); err != nil {
		t.Fatalf("set TLS_KEY -: %v", err)
	}
	if got := r.mustRun("get", "TLS_KEY"); got != pem {
		t.Fatalf("multi-line round-trip:\n got:  %q\n want: %q", got, pem)
	}
}

func TestSetNoValueNonTTYErrors(t *testing.T) {
	r := newRunner(t)
	// No value and a non-interactive stdin: must error (not silently read one line).
	_, se := r.mustFail("set", "K")
	has(t, se, "vars set K -")
}

func TestSetValueStartingWithDash(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "DASHED", "--", "-----BEGIN-----")
	if got := r.mustRun("get", "DASHED"); got != "-----BEGIN-----" {
		t.Fatalf("get = %q", got)
	}
}

func TestResolveRejectsInjectableManifestName(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SAFE", "ok")
	r.writeFile(".vars.yaml", "keys:\n  - \"EVIL$(touch pwned)\"\n")
	_, se := r.mustFail("resolve")
	has(t, se, "invalid env var name")
	if _, err := os.Stat(filepath.Join(r.workDir, "pwned")); err == nil {
		t.Fatal("injection guard failed: resolve must never emit an unsafe name")
	}
}

func TestResolveSkipsInjectableStdinName(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "SAFE", "ok")
	r.writeFile(".vars.yaml", "keys:\n  - SAFE\n")
	so, se, err := r.runStdin("BAD$(x)=v\nGOOD=p\n", "resolve", "--partial")
	if err != nil {
		t.Fatalf("resolve --partial: %v\n%s", err, se)
	}
	has(t, so, "export GOOD='p'") // valid pass-through still emitted
	has(t, se, "skipping invalid env var name")
	if strings.Contains(so, "BAD$(x)") {
		t.Fatalf("unsafe stdin name must not be emitted:\n%s", so)
	}
}

func TestResolveDotenvRejectsNewline(t *testing.T) {
	r := newRunner(t)
	r.runStdin("a\nb", "set", "PEM", "-")
	r.writeFile(".vars.yaml", "keys:\n  - PEM\n")
	_, se := r.mustFail("resolve", "--dotenv")
	has(t, se, "not representable in --dotenv")
	// posix is fine with newlines (literal in single quotes).
	has(t, r.mustRun("resolve"), "export PEM='a\nb'")
}

func TestDumpResilientToDotenvNewline(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "NORMAL", "ok")
	r.runStdin("a\nb", "set", "PEM", "-")
	// --dotenv can't represent the multi-line value: skip+warn+nonzero, keep the rest.
	so, se := r.mustFail("dump", "--dotenv")
	has(t, so, "NORMAL=ok")
	has(t, se, "skipping")
	// posix dump handles both.
	all := r.mustRun("dump")
	has(t, all, "export NORMAL='ok'")
	has(t, all, "export PEM='a\nb'")
}

// --- low-severity polish (2026-06-15 audit) ---

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func TestGitPassthroughExitCode(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "K", "v") // create the store (and its git repo)
	args := []string{"rev-parse", "--verify", "definitely-not-a-ref"}
	_, _, verr := r.run(append([]string{"git"}, args...)...)
	direct := exec.Command("git", append([]string{"-C", r.storeDir}, args...)...)
	direct.Run()
	want := direct.ProcessState.ExitCode()
	if want == 0 {
		t.Fatal("setup: a bad ref should make git exit non-zero")
	}
	if got := exitCode(verr); got != want {
		t.Fatalf("vars git exit = %d, want git's %d (must mirror git, not flatten to 1)", got, want)
	}
}

func TestRmDuplicateArgs(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "K", "v")
	r.mustRun("rm", "--force", "K", "K") // dedup: must not error on the repeat
	if _, _, err := r.run("get", "K"); err == nil {
		t.Fatal("K should be deleted")
	}
}

func TestMvSameKey(t *testing.T) {
	r := newRunner(t)
	r.mustRun("set", "K", "v")
	_, se := r.mustFail("mv", "--force", "K", "K")
	has(t, se, "source and destination are the same")
}

func TestSetManifestHintUsesParser(t *testing.T) {
	r := newRunner(t)
	// Quoted entry: the old "- KEY" substring matcher missed this and falsely warned.
	r.writeFile(".vars.yaml", "keys:\n  - \"LISTED\"\n")
	if _, se, _ := r.run("set", "LISTED", "v"); strings.Contains(se, "not listed") {
		t.Fatalf("a quoted manifest key should count as listed:\n%s", se)
	}
	if _, se, _ := r.run("set", "UNLISTED", "v"); !strings.Contains(se, "not listed in .vars.yaml") {
		t.Fatalf("an unlisted key should warn:\n%s", se)
	}
}

func TestFirstRunBackupTip(t *testing.T) {
	r := newRunner(t)
	_, se, err := r.run("set", "K", "v") // first command creates the store
	if err != nil {
		t.Fatalf("set: %v\n%s", err, se)
	}
	if _, e := os.Stat(filepath.Join(r.storeDir, ".git")); e != nil {
		t.Skip("git repo not initialized in this environment")
	}
	has(t, se, "versioned with git")         // message reflects that git is active
	has(t, se, "vars git remote add origin") // one-time backup nudge
}
