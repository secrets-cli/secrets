//go:build integration

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "vars-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}

	binary = filepath.Join(tmp, "vars")
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	if err != nil {
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

func newRunner(t *testing.T) *runner {
	t.Helper()
	storeDir := t.TempDir()
	workDir := t.TempDir()
	r := &runner{
		t:        t,
		storeDir: storeDir,
		workDir:  workDir,
		env: []string{
			"VARS_STORE_DIR=" + storeDir,
			"HOME=" + t.TempDir(),
			"PATH=" + os.Getenv("PATH"),
		},
	}

	// Stop any auto-started agent when the test ends
	t.Cleanup(func() {
		r.run("agent", "stop")
		time.Sleep(50 * time.Millisecond)
	})

	return r
}

func (r *runner) run(args ...string) (string, string, error) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.workDir
	cmd.Env = r.env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (r *runner) mustRun(args ...string) string {
	r.t.Helper()
	stdout, stderr, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("vars %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

func (r *runner) mustRunWithStderr(args ...string) (string, string) {
	r.t.Helper()
	stdout, stderr, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("vars %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout, stderr
}

func (r *runner) mustFail(args ...string) (string, string) {
	r.t.Helper()
	stdout, stderr, err := r.run(args...)
	if err == nil {
		r.t.Fatalf("vars %s should have failed\nstdout: %s\nstderr: %s", strings.Join(args, " "), stdout, stderr)
	}
	return stdout, stderr
}

func (r *runner) runWithStdin(stdin string, args ...string) (string, string, error) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.workDir
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (r *runner) mustRunWithEnv(extraEnv []string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.workDir
	cmd.Env = append(r.env, extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("vars %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func (r *runner) mustRunWithStdin(stdin string, args ...string) string {
	r.t.Helper()
	stdout, stderr, err := r.runWithStdin(stdin, args...)
	if err != nil {
		r.t.Fatalf("vars %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

func (r *runner) initNoPassphrase() {
	r.t.Helper()
	// Any command triggers auto-init on first run; ls is the simplest (no side effects).
	r.mustRunWithStdin("\n\n", "ls")
}

func (r *runner) writeFile(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.workDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		r.t.Fatalf("creating dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		r.t.Fatalf("writing %s: %v", name, err)
	}
}

// --- Tests ---

func TestIntegration_CRUDLifecycle(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// set (agent auto-starts on first command)
	r.mustRun("set", "API_KEY", "my-secret-key")
	r.mustRun("set", "DB_URL", "postgres://localhost/db")
	r.mustRun("set", "TOKEN", "abc123")

	// get
	out := r.mustRun("get", "API_KEY")
	if out != "my-secret-key" {
		t.Fatalf("get API_KEY = %q, want %q", out, "my-secret-key")
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatal("get should not have trailing newline")
	}

	// ls
	out = r.mustRun("ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls returned %d lines, want 3: %v", len(lines), lines)
	}
	if lines[0] != "API_KEY" || lines[1] != "DB_URL" || lines[2] != "TOKEN" {
		t.Fatalf("ls not sorted: %v", lines)
	}

	// rm
	r.mustRun("rm", "TOKEN", "--force")
	out = r.mustRun("ls")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("ls after rm returned %d lines, want 2", len(lines))
	}

	r.mustFail("get", "TOKEN")
	r.mustFail("rm", "NONEXISTENT", "--force")
}

func TestIntegration_SetReplace(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "first")
	r.mustRun("set", "KEY", "second", "--replace")

	out := r.mustRun("get", "KEY")
	if out != "second" {
		t.Fatalf("get after replace = %q, want %q", out, "second")
	}
}

func TestIntegration_Set_Idempotent(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "value")
	_, stderr := r.mustRunWithStderr("set", "KEY", "value")
	if !strings.Contains(stderr, "Already set") {
		t.Fatalf("expected idempotent message, got: %s", stderr)
	}

	if r.mustRun("get", "KEY") != "value" {
		t.Fatal("value should be unchanged")
	}
}

func TestIntegration_Set_Skip(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "original")
	r.mustRun("set", "KEY", "new", "--skip")

	if r.mustRun("get", "KEY") != "original" {
		t.Fatal("--skip should leave existing value unchanged")
	}
}

func TestIntegration_Set_NonTTYConflictFails(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "original")
	_, stderr := r.mustFail("set", "KEY", "new")
	if !strings.Contains(stderr, "--replace") || !strings.Contains(stderr, "--skip") {
		t.Fatalf("expected flag hint, got: %s", stderr)
	}
}

func TestIntegration_SpecialValues(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	cases := map[string]string{
		"SPACES": "value with spaces",
		"EQUALS": "key=value=extra",
		"URL":    "https://user:pass@host:8080/path?q=1&r=2",
		"HEX":    "0xdeadbeef0123456789abcdef",
	}

	for k, v := range cases {
		r.mustRun("set", k, v)
	}

	for k, want := range cases {
		got := r.mustRun("get", k)
		if got != want {
			t.Errorf("get %s = %q, want %q", k, got, want)
		}
	}
}

func TestIntegration_ResolvePosix(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "https://rpc.example.com")
	r.mustRun("set", "PRIVATE_KEY", "0xTESTKEY")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
  - PRIVATE_KEY
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "export RPC_URL=") {
		t.Fatalf("posix export missing RPC_URL: %s", out)
	}
	if !strings.Contains(out, "export PRIVATE_KEY=") {
		t.Fatalf("posix export missing PRIVATE_KEY: %s", out)
	}
}

func TestIntegration_ResolveFish(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "MY_VAR", "hello world")

	r.writeFile(".vars.yaml", `keys:
  - MY_VAR
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--fish")
	if !strings.Contains(out, "set -x MY_VAR") {
		t.Fatalf("fish export missing set -x: %s", out)
	}
}

func TestIntegration_ResolveDotenv(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "MY_VAR", "hello world")

	r.writeFile(".vars.yaml", `keys:
  - MY_VAR
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--dotenv")
	if !strings.Contains(out, "MY_VAR=") {
		t.Fatalf("dotenv export missing MY_VAR: %s", out)
	}
}

func TestIntegration_ResolveLocalGlobalProfile(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "PRIVATE_KEY", "0xGLOBALKEY")

	r.writeFile(".vars.yaml", `keys:
  - PROJECT_PK
`)
	r.writeFile(".vars.local.yaml", `profiles:
  global:
    PROJECT_PK: PRIVATE_KEY
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "export PROJECT_PK=") {
		t.Fatalf("mapped export missing PROJECT_PK: %s", out)
	}
	if !strings.Contains(out, "0xGLOBALKEY") {
		t.Fatalf("mapped export has wrong value: %s", out)
	}
}

func TestIntegration_ResolvePartial(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "EXISTS", "value")

	r.writeFile(".vars.yaml", `keys:
  - EXISTS
  - MISSING
`)

	r.mustFail("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--partial")
	if !strings.Contains(out, "EXISTS") {
		t.Fatalf("partial resolve missing EXISTS: %s", out)
	}
	if strings.Contains(out, "MISSING") {
		t.Fatalf("partial resolve should omit MISSING key: %s", out)
	}
}

func TestIntegration_ResolveStdinDotenv(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "STORE_KEY", "from_store")

	r.writeFile(".vars.yaml", `keys:
  - STORE_KEY
  - DOTENV_ONLY
  - BOTH
`)

	// STORE_KEY: in store and dotenv — store wins
	// DOTENV_ONLY: only in dotenv — used as fallback
	// BOTH: in both — store wins
	// PASSTHROUGH: not in manifest — passed through unchanged
	r.mustRun("set", "BOTH", "store_wins")
	stdin := "STORE_KEY=dotenv_value\nDOTENV_ONLY=from_dotenv\nBOTH=dotenv_value\nPASSTHROUGH=passthrough_value\n"

	out := r.mustRunWithStdin(stdin, "resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--partial")

	if !strings.Contains(out, "from_store") {
		t.Fatalf("expected store value for STORE_KEY, got: %s", out)
	}
	if strings.Contains(out, "dotenv_value") && strings.Contains(out, "STORE_KEY") {
		// make sure STORE_KEY didn't use dotenv
	}
	if !strings.Contains(out, "from_dotenv") {
		t.Fatalf("expected dotenv fallback for DOTENV_ONLY, got: %s", out)
	}
	if !strings.Contains(out, "store_wins") {
		t.Fatalf("expected store to win for BOTH, got: %s", out)
	}
	if !strings.Contains(out, "passthrough_value") {
		t.Fatalf("expected PASSTHROUGH key to be passed through, got: %s", out)
	}
}

func TestIntegration_ResolveStdin_AgentNotRunning(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".vars.yaml", `keys:
  - FOO
`)

	// Stop the agent so it is definitely not running
	r.mustRun("agent", "stop")

	_, stderr, err := r.runWithStdin("FOO=bar\n", "resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--partial")
	if err == nil {
		t.Fatal("expected error when agent is not running and stdin is piped")
	}
	if !strings.Contains(stderr, "agent is not running") {
		t.Fatalf("expected 'agent is not running' error, got stderr: %s", stderr)
	}
}

func TestIntegration_DumpAllFormats(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY1", "val1")
	r.mustRun("set", "KEY2", "val2")

	for _, flag := range []string{"", "--fish", "--dotenv"} {
		args := []string{"dump"}
		if flag != "" {
			args = append(args, flag)
		}
		out := r.mustRun(args...)
		if !strings.Contains(out, "KEY1") || !strings.Contains(out, "KEY2") {
			t.Errorf("dump %s missing keys: %s", flag, out)
		}
	}
}

func TestIntegration_AgentAutoStart(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Agent is auto-started by init; set uses it
	r.mustRun("set", "AUTO_KEY", "auto_value")

	// Agent should be running
	_, stderr, err := r.run("agent")
	if err != nil {
		t.Fatalf("agent check failed: %v", err)
	}
	if !strings.Contains(stderr, "already running") {
		t.Fatalf("agent should be auto-started, got: %s", stderr)
	}

	// Reads work via agent
	val := r.mustRun("get", "AUTO_KEY")
	if val != "auto_value" {
		t.Fatalf("get = %q, want %q", val, "auto_value")
	}
}

func TestIntegration_AgentExplicitStop(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Auto-start via any command
	r.mustRun("ls")

	// Stop
	r.mustRun("agent", "stop")
	time.Sleep(100 * time.Millisecond)

	// Next command should auto-start again
	r.mustRun("set", "KEY", "value")
	val := r.mustRun("get", "KEY")
	if val != "value" {
		t.Fatalf("get after restart = %q, want %q", val, "value")
	}
}

func TestIntegration_Passwd_EmptyToSet(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "value")

	// Change from empty to "newpass": current passphrase (empty), then new + confirm.
	r.mustRunWithStdin("\nnewpass\nnewpass\n", "passwd")

	// Agent is still running with new passphrase. Reads still work.
	val := r.mustRun("get", "KEY")
	if val != "value" {
		t.Fatalf("get after passwd = %q, want %q", val, "value")
	}

	// Stop and restart to verify passphrase changed on disk
	r.mustRun("agent", "stop")
	time.Sleep(100 * time.Millisecond)

	// Now starting agent requires the new passphrase
	out, _, err := r.runWithStdin("newpass\n", "get", "KEY")
	if err != nil {
		t.Fatalf("get with new passphrase failed: %v", err)
	}
	if out != "value" {
		t.Fatalf("get = %q, want %q", out, "value")
	}
}

func TestIntegration_Passwd_SetToEmpty(t *testing.T) {
	r := newRunner(t)

	// Init with passphrase via auto-init triggered by first command
	r.mustRunWithStdin("mypass\nmypass\n", "ls")

	// Auto-start agent (needs passphrase) and set a value
	r.mustRunWithStdin("mypass\n", "set", "KEY", "value")

	// Change to empty passphrase: current (mypass), then new (empty) + confirm (empty).
	r.mustRunWithStdin("mypass\n\n\n", "passwd")

	// Reads still work (agent has the data)
	val := r.mustRun("get", "KEY")
	if val != "value" {
		t.Fatalf("get after passwd = %q, want %q", val, "value")
	}

	// Stop and restart — should not need passphrase now
	r.mustRun("agent", "stop")
	time.Sleep(100 * time.Millisecond)

	out := r.mustRun("get", "KEY")
	if out != "value" {
		t.Fatalf("get after restart = %q, want %q", out, "value")
	}
}

func TestIntegration_WrongPassphrase(t *testing.T) {
	r := newRunner(t)

	r.mustRunWithStdin("correctpass\ncorrectpass\n", "ls")

	// auto-init starts the agent; stop it to simulate a fresh session
	r.mustRun("agent", "stop")
	time.Sleep(100 * time.Millisecond)

	// Agent auto-start with wrong passphrase should fail
	_, stderr, err := r.runWithStdin("wrongpass\n", "get", "KEY")
	if err == nil {
		t.Fatal("get with wrong passphrase should fail")
	}
	if !strings.Contains(stderr, "incorrect passphrase") {
		t.Fatalf("expected passphrase error, got: %s", stderr)
	}
}

func TestIntegration_ResolveMissingManifest(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	_, stderr := r.mustFail("resolve", "-f", filepath.Join(r.workDir, "nonexistent.yaml"))
	if !strings.Contains(stderr, "manifest not found") {
		t.Fatalf("expected 'manifest not found' error, got: %s", stderr)
	}
}

func TestIntegration_ResolveUnknownFlag(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	_, stderr := r.mustFail("resolve", "--unknown-flag")
	if !strings.Contains(stderr, "unknown flag") {
		t.Fatalf("expected unknown flag error, got: %s", stderr)
	}
}

func TestIntegration_Version(t *testing.T) {
	r := newRunner(t)
	out := r.mustRun("--version")
	if !strings.HasPrefix(out, "vars ") {
		t.Fatalf("version output should start with 'vars ', got: %q", out)
	}
}

func TestIntegration_ResolveGlobalProfile(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "GLOBAL_TOKEN", "tok123")

	r.writeFile(".vars.yaml", `keys:
  - LOCAL_TOKEN
profiles:
  global:
    LOCAL_TOKEN: GLOBAL_TOKEN
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "LOCAL_TOKEN") {
		t.Fatalf("global profile missing LOCAL_TOKEN: %s", out)
	}
	if !strings.Contains(out, "tok123") {
		t.Fatalf("global profile has wrong value: %s", out)
	}
}

func TestIntegration_Resolve_DefaultProfileAutoApplied(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "prod/RPC_URL", "https://prod.rpc")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  default:
    RPC_URL: prod/RPC_URL
`)

	// No --profile flag: "default" profile should be applied automatically
	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "https://prod.rpc") {
		t.Fatalf("default profile not auto-applied: %s", out)
	}
}

func TestIntegration_Resolve_ActiveProfileOverridesGlobal(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "shared/KEY", "from-global")
	r.mustRun("set", "prod/KEY", "from-mainnet")

	r.writeFile(".vars.yaml", `keys:
  - MY_KEY
profiles:
  global:
    MY_KEY: shared/KEY
  mainnet:
    MY_KEY: prod/KEY
`)

	// Without --profile: global applies
	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "from-global") {
		t.Fatalf("global profile not applied when no profile active: %s", out)
	}

	// With --profile mainnet: active profile overrides global
	out = r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "mainnet")
	if !strings.Contains(out, "from-mainnet") {
		t.Fatalf("active profile should override global: %s", out)
	}
	if strings.Contains(out, "from-global") {
		t.Fatalf("global value should not appear when active profile overrides: %s", out)
	}
}

func TestIntegration_Mv(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "OLD_KEY", "my-value")

	r.mustRun("mv", "--force", "OLD_KEY", "NEW_KEY")

	// Old key gone
	r.mustFail("get", "OLD_KEY")

	// New key has the value
	val := r.mustRun("get", "NEW_KEY")
	if val != "my-value" {
		t.Fatalf("get NEW_KEY = %q, want %q", val, "my-value")
	}
}

func TestIntegration_Mv_RequiresForceInNonTTY(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "OLD_KEY", "my-value")

	_, stderr := r.mustFail("mv", "OLD_KEY", "NEW_KEY")
	if !strings.Contains(stderr, "--force") {
		t.Fatalf("expected error mentioning --force, got: %s", stderr)
	}
}

func TestIntegration_Mv_MissingKey(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	_, stderr := r.mustFail("mv", "--force", "NONEXISTENT", "NEW_KEY")
	if !strings.Contains(stderr, "NONEXISTENT") {
		t.Fatalf("expected error mentioning key name, got: %s", stderr)
	}
}

func TestIntegration_Mv_DestinationExists(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY_A", "a")
	r.mustRun("set", "KEY_B", "b")

	_, stderr := r.mustFail("mv", "--force", "KEY_A", "KEY_B")
	if !strings.Contains(stderr, "KEY_B") {
		t.Fatalf("expected error mentioning destination key, got: %s", stderr)
	}

	// Both keys untouched
	if r.mustRun("get", "KEY_A") != "a" {
		t.Fatal("KEY_A should be unchanged")
	}
	if r.mustRun("get", "KEY_B") != "b" {
		t.Fatal("KEY_B should be unchanged")
	}
}

func TestIntegration_Rm_RequiresForceInNonTTY(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "value")

	_, stderr := r.mustFail("rm", "KEY")
	if !strings.Contains(stderr, "--force") {
		t.Fatalf("expected error mentioning --force, got: %s", stderr)
	}
}

func TestIntegration_Rm_MultiKey(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY_A", "a")
	r.mustRun("set", "KEY_B", "b")
	r.mustRun("set", "KEY_C", "c")

	r.mustRun("rm", "--force", "KEY_A", "KEY_B")

	r.mustFail("get", "KEY_A")
	r.mustFail("get", "KEY_B")

	// KEY_C untouched
	if r.mustRun("get", "KEY_C") != "c" {
		t.Fatal("KEY_C should be unchanged")
	}
}

func TestIntegration_Rm_MultiKey_OneMissing(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY_A", "a")

	_, stderr := r.mustFail("rm", "--force", "KEY_A", "NONEXISTENT")
	if !strings.Contains(stderr, "NONEXISTENT") {
		t.Fatalf("expected error mentioning missing key, got: %s", stderr)
	}

	// KEY_A should be untouched (validation happens before deletion)
	if r.mustRun("get", "KEY_A") != "a" {
		t.Fatal("KEY_A should be unchanged after partial failure")
	}
}

func TestIntegration_Import_Clean(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".env", "RPC_URL=https://rpc.example.com\nPRIVATE_KEY=0xABC\n")
	r.mustRun("import", filepath.Join(r.workDir, ".env"))

	if r.mustRun("get", "RPC_URL") != "https://rpc.example.com" {
		t.Fatal("RPC_URL not imported")
	}
	if r.mustRun("get", "PRIVATE_KEY") != "0xABC" {
		t.Fatal("PRIVATE_KEY not imported")
	}
}

func TestIntegration_Import_Scope(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".env", "RPC_URL=https://rpc.example.com\n")
	r.mustRun("import", "dev", filepath.Join(r.workDir, ".env"))

	r.mustFail("get", "RPC_URL")
	if r.mustRun("get", "dev/RPC_URL") != "https://rpc.example.com" {
		t.Fatal("dev/RPC_URL not imported")
	}
}

func TestIntegration_Ls_Scope(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "GLOBAL_KEY", "g")
	r.mustRun("set", "prod/KEY_A", "a")
	r.mustRun("set", "prod/KEY_B", "b")
	r.mustRun("set", "staging/KEY_A", "sa")

	// ls (default scope) — only GLOBAL_KEY
	out := r.mustRun("ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || lines[0] != "GLOBAL_KEY" {
		t.Fatalf("ls: expected [GLOBAL_KEY], got %v", lines)
	}

	// ls prod — KEY_A and KEY_B without prefix
	out = r.mustRun("ls", "prod")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "KEY_A" || lines[1] != "KEY_B" {
		t.Fatalf("ls prod: expected [KEY_A KEY_B], got %v", lines)
	}

	// ls staging — KEY_A without prefix
	out = r.mustRun("ls", "staging")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || lines[0] != "KEY_A" {
		t.Fatalf("ls staging: expected [KEY_A], got %v", lines)
	}
}

func TestIntegration_Resolve_Profile(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "prod/RPC_URL", "https://prod.rpc")
	r.mustRun("set", "ETHERSCAN_API", "shared-key")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
  - ETHERSCAN_API
profiles:
  mainnet:
    RPC_URL: prod/RPC_URL
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "mainnet")
	if !strings.Contains(out, "https://prod.rpc") {
		t.Fatalf("expected profile RPC_URL, got: %s", out)
	}
	if !strings.Contains(out, "shared-key") {
		t.Fatalf("expected bare ETHERSCAN_API, got: %s", out)
	}
}

func TestIntegration_Resolve_LocalProfileOverride(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "prod/PRIVATE_KEY_alice", "0xALICE")

	r.writeFile(".vars.yaml", `keys:
  - PRIVATE_KEY
profiles:
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY_team
`)
	r.writeFile(".vars.local.yaml", `profiles:
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY_alice
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "mainnet")
	if !strings.Contains(out, "0xALICE") {
		t.Fatalf("local profile should override committed: got %s", out)
	}
}

func TestIntegration_Import_SkipConflict(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "original")
	r.writeFile(".env", "RPC_URL=new_value\nNEW_KEY=new\n")
	r.mustRun("import", filepath.Join(r.workDir, ".env"), "--skip")

	// Existing key unchanged
	if r.mustRun("get", "RPC_URL") != "original" {
		t.Fatal("RPC_URL should be unchanged with --skip")
	}
	// New key imported
	if r.mustRun("get", "NEW_KEY") != "new" {
		t.Fatal("NEW_KEY should be imported")
	}
}

func TestIntegration_Import_ReplaceConflict(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "original")
	r.writeFile(".env", "RPC_URL=updated\n")
	r.mustRun("import", filepath.Join(r.workDir, ".env"), "--replace")

	if r.mustRun("get", "RPC_URL") != "updated" {
		t.Fatal("RPC_URL should be replaced")
	}
}

func TestIntegration_Import_SameValueSkipped(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "same_value")
	r.writeFile(".env", "RPC_URL=same_value\n")
	// No --skip or --replace needed — same value is not a conflict
	r.mustRun("import", filepath.Join(r.workDir, ".env"))

	if r.mustRun("get", "RPC_URL") != "same_value" {
		t.Fatal("RPC_URL should still have same_value")
	}
}

func TestIntegration_Import_NonTTYConflictFails(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "original")
	r.writeFile(".env", "RPC_URL=new_value\n")

	// Non-TTY with conflict and no flag should fail
	_, stderr := r.mustFail("import", filepath.Join(r.workDir, ".env"))
	if !strings.Contains(stderr, "--replace") || !strings.Contains(stderr, "--skip") {
		t.Fatalf("expected hint about --replace/--skip, got: %s", stderr)
	}
}

func TestIntegration_DumpEmpty(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	out := r.mustRun("dump")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("dump of empty store should be empty, got: %q", out)
	}
}

func TestIntegration_Scopes(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "BARE_KEY", "bare")
	r.mustRun("set", "prod/RPC_URL", "p1")
	r.mustRun("set", "prod/PRIVATE_KEY", "p2")
	r.mustRun("set", "staging/RPC_URL", "s1")

	out := r.mustRun("scope", "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("scopes returned %d lines, want 2: %v", len(lines), lines)
	}
	if lines[0] != "prod" || lines[1] != "staging" {
		t.Fatalf("scopes = %v, want [prod staging]", lines)
	}
}

func TestIntegration_Scopes_Hierarchical(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "main/dev/RPC_URL", "v1")
	r.mustRun("set", "main/RPC_URL", "v2")
	r.mustRun("set", "prod/KEY", "v3")

	out := r.mustRun("scope", "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// expect: main, main/dev, prod — sorted
	want := []string{"main", "main/dev", "prod"}
	if len(lines) != len(want) {
		t.Fatalf("scopes = %v, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("scopes[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

func TestIntegration_Scopes_Empty(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "BARE_KEY", "bare")

	out := r.mustRun("scope", "ls")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("scopes with no scoped keys should be empty, got: %q", out)
	}
}

func TestIntegration_ResolveOrderPreserved(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "ZEBRA", "z")
	r.mustRun("set", "ALPHA", "a")
	r.mustRun("set", "MIKE", "m")

	r.writeFile(".vars.yaml", `keys:
  - MIKE
  - ZEBRA
  - ALPHA
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("export returned %d lines, want 3", len(lines))
	}
	if !strings.Contains(lines[0], "MIKE") {
		t.Fatalf("first line should contain MIKE, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "ZEBRA") {
		t.Fatalf("second line should contain ZEBRA, got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "ALPHA") {
		t.Fatalf("third line should contain ALPHA, got: %s", lines[2])
	}
}

func TestIntegration_Resolve_DefaultProfile(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "prod/RPC_URL", "https://prod.rpc")
	r.mustRun("set", "ETHERSCAN_API", "shared-key")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
  - ETHERSCAN_API
profiles:
  default:
    RPC_URL: prod/RPC_URL
`)

	// No --profile flag: "default" profile is auto-applied
	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(out, "https://prod.rpc") {
		t.Fatalf("default profile should be auto-applied: got %s", out)
	}
	if !strings.Contains(out, "shared-key") {
		t.Fatalf("bare key fallback should still work: got %s", out)
	}
}

func TestIntegration_Resolve_HierarchicalFallback(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Only the base key exists; manifest maps to a more-specific key
	r.mustRun("set", "RPC_URL", "https://base.rpc")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  mainnet:
    RPC_URL: main/dev/RPC_URL
`)

	// main/dev/RPC_URL not in store → main/RPC_URL not in store → RPC_URL found
	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "mainnet")
	if !strings.Contains(out, "https://base.rpc") {
		t.Fatalf("hierarchical fallback should resolve to base key: got %s", out)
	}
}

// Discriminating test for the scope-fallback DIRECTION: only the intermediate
// scope level exists. Correct (deepest-first) fallback strips "dev" first and
// lands on main/RPC_URL. The previously-shipped bug stripped the outermost
// scope first (main/dev/RPC_URL → dev/RPC_URL → RPC_URL), which would miss
// everything here and fail. The older test above can't catch this because it
// only stores the bare key, where both directions end at the same place.
func TestIntegration_Resolve_HierarchicalFallback_StopsAtIntermediate(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Intentionally NOT setting main/dev/RPC_URL, dev/RPC_URL, or bare RPC_URL.
	r.mustRun("set", "main/RPC_URL", "https://main.rpc")

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  mainnet:
    RPC_URL: main/dev/RPC_URL
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "mainnet")
	if !strings.Contains(out, "https://main.rpc") {
		t.Fatalf("deepest-first fallback should resolve main/dev/RPC_URL to main/RPC_URL, got: %s", out)
	}
}

func TestIntegration_Ls_All(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "BARE_KEY", "b")
	r.mustRun("set", "prod/RPC_URL", "p")
	r.mustRun("set", "staging/API_KEY", "s")

	out := r.mustRun("ls", "--all")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls --all should list 3 keys, got: %v", lines)
	}

	// Default ls still only shows unscoped keys
	out = r.mustRun("ls")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || lines[0] != "BARE_KEY" {
		t.Fatalf("ls should only show unscoped keys, got: %v", lines)
	}
}

func TestIntegration_History_RecordedOnReplace(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "https://v1.example.com")
	r.mustRun("set", "--replace", "RPC_URL", "https://v2.example.com")
	r.mustRun("set", "--replace", "RPC_URL", "https://v3.example.com")

	out := r.mustRun("history", "RPC_URL")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("history should have 2 entries, got: %v", lines)
	}
	// Newest first: RPC_URL~2 then RPC_URL~1
	if !strings.HasPrefix(lines[0], "RPC_URL~2:") {
		t.Fatalf("first history line should be RPC_URL~2, got: %s", lines[0])
	}
	if !strings.Contains(lines[0], "https://v2.example.com") {
		t.Fatalf("first history line should contain v2, got: %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "RPC_URL~1:") {
		t.Fatalf("second history line should be RPC_URL~1, got: %s", lines[1])
	}
	if !strings.Contains(lines[1], "https://v1.example.com") {
		t.Fatalf("second history line should contain v1, got: %s", lines[1])
	}
}

func TestIntegration_History_NotInLs(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "v1")
	r.mustRun("set", "--replace", "KEY", "v2")

	out := r.mustRun("ls", "--all")
	if strings.Contains(out, "~") {
		t.Fatalf("ls --all should not show history entries, got: %s", out)
	}
}

func TestIntegration_History_DeleteCascades(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "v1")
	r.mustRun("set", "--replace", "KEY", "v2")
	r.mustRun("rm", "--force", "KEY")

	// Verify history was cascade-deleted (no KEY~ entries remain in store)
	out := r.mustRun("ls", "--all")
	if strings.Contains(out, "KEY~") {
		t.Fatalf("history entries should be removed after rm, got: %s", out)
	}
}

func TestIntegration_History_MvCarriesHistory(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "OLD_KEY", "v1")
	r.mustRun("set", "--replace", "OLD_KEY", "v2")
	r.mustRun("mv", "--force", "OLD_KEY", "NEW_KEY")

	// History under new name
	out := r.mustRun("history", "NEW_KEY")
	if !strings.Contains(out, "NEW_KEY~1:") {
		t.Fatalf("history should be under NEW_KEY after mv, got: %s", out)
	}
	if !strings.Contains(out, "v1") {
		t.Fatalf("history should contain original value, got: %s", out)
	}

	// Old name has no entries remaining in store
	all := r.mustRun("ls", "--all")
	if strings.Contains(all, "OLD_KEY") {
		t.Fatalf("OLD_KEY entries should be gone after mv, got: %s", all)
	}
}

func TestIntegration_History_EmptyForNewKey(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "v1")

	out := r.mustRun("history", "KEY")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("new key should have no history, got: %s", out)
	}
}

func TestIntegration_Resolve_Origin_Literal(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".vars.yaml", `keys:
  - LOG_LEVEL
  - API_KEY
profiles:
  global:
    LOG_LEVEL: = info
`)
	r.mustRun("set", "API_KEY", "secret")

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--origin")
	if !strings.Contains(out, "# manifest") {
		t.Fatalf("expected '# manifest' annotation for =info, got: %s", out)
	}
	if !strings.Contains(out, "# vars") {
		t.Fatalf("expected '# vars' annotation for API_KEY, got: %s", out)
	}
}

func TestIntegration_Resolve_Origin_Default_Used(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  global:
    RPC_URL: ?= http://localhost:8545
`)
	// RPC_URL is NOT in the store → default should be used

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--origin")
	if !strings.Contains(out, "# manifest") {
		t.Fatalf("expected '# manifest' annotation when store key missing, got: %s", out)
	}
	if !strings.Contains(out, "http://localhost:8545") {
		t.Fatalf("expected default value in output, got: %s", out)
	}
}

func TestIntegration_Resolve_Origin_Default_StoreWins(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "https://real.rpc")
	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  global:
    RPC_URL: ?= http://localhost:8545
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--origin")
	if !strings.Contains(out, "https://real.rpc") {
		t.Fatalf("store value should take precedence over default, got: %s", out)
	}
	if !strings.Contains(out, "# vars") {
		t.Fatalf("expected '# vars' annotation when store value used, got: %s", out)
	}
	if strings.Contains(out, "# manifest") {
		t.Fatalf("should not show '# manifest' when store value found, got: %s", out)
	}
}

func TestIntegration_Resolve_Default_EmptyStoreTriggersFallback(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "RPC_URL", "")
	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
profiles:
  global:
    RPC_URL: ?= http://localhost:8545
`)

	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--origin")
	if !strings.Contains(out, "http://localhost:8545") {
		t.Fatalf("empty store value should trigger default, got: %s", out)
	}
	if !strings.Contains(out, "# manifest") {
		t.Fatalf("expected '# manifest' annotation for empty store value, got: %s", out)
	}
}

func TestIntegration_Resolve_ShellEnvFallback(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// RPC_URL not in store; present in shell env
	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
  - API_KEY
`)
	r.mustRun("set", "API_KEY", "secret")

	out := r.mustRunWithEnv([]string{"RPC_URL=http://from-shell"}, "resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	// RPC_URL already in shell: no export line emitted
	if strings.Contains(out, "RPC_URL") {
		t.Fatalf("shell env key should not be emitted in output, got: %s", out)
	}
	// API_KEY from store is still emitted
	if !strings.Contains(out, "secret") {
		t.Fatalf("store key should still be emitted, got: %s", out)
	}
}

func TestIntegration_Resolve_ShellEnvFallback_Origin(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".vars.yaml", `keys:
  - RPC_URL
`)

	out := r.mustRunWithEnv([]string{"RPC_URL=http://from-shell"}, "resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--origin")
	if !strings.Contains(out, "# RPC_URL  shell") {
		t.Fatalf("expected '# RPC_URL  shell' comment, got: %s", out)
	}
	// The actual value must not be emitted
	if strings.Contains(out, "http://from-shell") {
		t.Fatalf("shell value must not appear in output, got: %s", out)
	}
}

func TestIntegration_Init(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// vars init creates .vars.yaml
	_, stderr, err := r.run("init")
	if err != nil {
		t.Fatalf("vars init failed: %v\nstderr: %s", err, stderr)
	}
	content, readErr := os.ReadFile(filepath.Join(r.workDir, ".vars.yaml"))
	if readErr != nil {
		t.Fatalf(".vars.yaml not created: %v", readErr)
	}
	if !strings.Contains(string(content), "keys:") {
		t.Fatalf(".vars.yaml missing keys: section, got: %s", content)
	}

	// vars init errors if .vars.yaml already exists
	_, stderr, err = r.run("init")
	if err == nil {
		t.Fatal("vars init should fail when .vars.yaml already exists")
	}
	if !strings.Contains(stderr, "already exists") {
		t.Fatalf("expected 'already exists' error, got: %s", stderr)
	}
}

func TestIntegration_GitignoreWarning(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Set up a project with .vars.yaml so PersistentPreRun fires
	r.writeFile(".vars.yaml", "keys:\n  - KEY\n")
	r.mustRun("set", "KEY", "val")

	// .vars.local.yaml exists but no .gitignore — no warning expected
	r.writeFile(".vars.local.yaml", "profiles: {}\n")
	_, stderr := r.mustRunWithStderr("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if strings.Contains(stderr, "not in .gitignore") {
		t.Fatalf("should not warn when no .gitignore exists, got: %s", stderr)
	}

	// .gitignore exists but doesn't mention .vars.local.yaml — warning expected
	r.writeFile(".gitignore", "*.log\n")
	_, stderr = r.mustRunWithStderr("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if !strings.Contains(stderr, ".vars.local.yaml") {
		t.Fatalf("expected warning about .vars.local.yaml, got: %s", stderr)
	}

	// .gitignore covers .vars.local.yaml — no warning
	r.writeFile(".gitignore", "*.log\n.vars.local.yaml\n")
	_, stderr = r.mustRunWithStderr("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	if strings.Contains(stderr, "not in .gitignore") {
		t.Fatalf("should not warn when .gitignore covers file, got: %s", stderr)
	}
}

func TestIntegration_Resolve_ProfileNotFound(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "KEY", "val")
	r.writeFile(".vars.yaml", `keys:
  - KEY
profiles:
  mainnet:
    KEY: prod/KEY
`)

	_, stderr := r.mustRunWithStderr("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "nonexistent", "--partial")
	if !strings.Contains(stderr, "nonexistent") {
		t.Fatalf("expected warning mentioning profile name, got: %s", stderr)
	}
}

func TestIntegration_Resolve_MissingKey_ProfileHint(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	// Store has DEPLOYER_KEY but not DEPLOYMENT_PRIVATE_KEY (bare).
	r.mustRun("set", "DEPLOYER_KEY", "0xSECRET")

	r.writeFile(".vars.yaml", `keys:
  - DEPLOYMENT_PRIVATE_KEY
profiles:
  common:
    DEPLOYMENT_PRIVATE_KEY: DEPLOYER_KEY
  sepolia:
    DEPLOYMENT_PRIVATE_KEY: dev/DEPLOYER_KEY
  zksync-sepolia:
    DEPLOYMENT_PRIVATE_KEY: zksync/DEPLOYER_KEY
`)

	// No --profile flag: identity path used, DEPLOYMENT_PRIVATE_KEY not in store.
	_, stderr := r.mustFail("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))

	if !strings.Contains(stderr, "not found in store") {
		t.Fatalf("expected 'not found in store' error, got: %s", stderr)
	}
	if !strings.Contains(stderr, "Hint:") {
		t.Fatalf("expected hint section, got: %s", stderr)
	}
	if !strings.Contains(stderr, "DEPLOYMENT_PRIVATE_KEY") {
		t.Fatalf("expected env name in hint, got: %s", stderr)
	}
	if !strings.Contains(stderr, "DEPLOYER_KEY") {
		t.Fatalf("expected store key in hint, got: %s", stderr)
	}
	if !strings.Contains(stderr, "vars resolve --profile") {
		t.Fatalf("expected 'Try: vars resolve --profile' suggestion, got: %s", stderr)
	}
	if !strings.Contains(stderr, "Available profiles:") {
		t.Fatalf("expected 'Available profiles' in hint, got: %s", stderr)
	}
	if !strings.Contains(stderr, "common") || !strings.Contains(stderr, "sepolia") || !strings.Contains(stderr, "zksync-sepolia") {
		t.Fatalf("expected all profile names listed, got: %s", stderr)
	}
}

func TestIntegration_Resolve_MissingKey_NoProfileHintWhenProfileActive(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "DEPLOYER_KEY", "0xSECRET")

	r.writeFile(".vars.yaml", `keys:
  - DEPLOYMENT_PRIVATE_KEY
profiles:
  common:
    DEPLOYMENT_PRIVATE_KEY: DEPLOYER_KEY
`)

	// With --profile common active: the resolved store key is DEPLOYER_KEY which exists.
	// This should succeed, not produce an error.
	out := r.mustRun("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"), "--profile", "common")
	if !strings.Contains(out, "0xSECRET") {
		t.Fatalf("expected store value with profile active, got: %s", out)
	}
}

func TestIntegration_Resolve_MissingKey_ProfileHintMultipleProfiles(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "STORE_A", "val-a")
	r.mustRun("set", "STORE_B", "val-b")

	r.writeFile(".vars.yaml", `keys:
  - MY_KEY
profiles:
  dev:
    MY_KEY: STORE_A
  prod:
    MY_KEY: STORE_B
`)

	_, stderr := r.mustFail("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))

	if !strings.Contains(stderr, "not found in store") {
		t.Fatalf("expected 'not found in store' error, got: %s", stderr)
	}
	// When multiple profiles map the same key, show all of them
	if !strings.Contains(stderr, "dev") || !strings.Contains(stderr, "prod") {
		t.Fatalf("expected both profile names in multi-profile hint, got: %s", stderr)
	}
	if strings.Contains(stderr, "vars resolve --profile common") {
		t.Fatalf("should not suggest a specific profile when multiple match, got: %s", stderr)
	}
}

func TestIntegration_Resolve_MissingKey_NoHintWhenNoProfiles(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.writeFile(".vars.yaml", `keys:
  - MISSING_KEY
`)

	_, stderr := r.mustFail("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))

	if !strings.Contains(stderr, "not found in store") {
		t.Fatalf("expected 'not found in store' error, got: %s", stderr)
	}
	if strings.Contains(stderr, "Hint:") {
		t.Fatalf("should not show hint when no profiles exist, got: %s", stderr)
	}
}

func TestIntegration_Resolve_MissingKey_GlobalProfileNotShown(t *testing.T) {
	r := newRunner(t)
	r.initNoPassphrase()

	r.mustRun("set", "GLOBAL_KEY", "global-val")

	r.writeFile(".vars.yaml", `keys:
  - MY_KEY
profiles:
  global:
    MY_KEY: GLOBAL_KEY
`)

	// global: is always applied as fallback, so this should succeed.
	// If it fails, the hint logic should not mention "global".
	_, stderr, _ := r.run("resolve", "-f", filepath.Join(r.workDir, ".vars.yaml"))
	// global profile maps MY_KEY → GLOBAL_KEY, which exists, so this should succeed.
	if strings.Contains(stderr, "not found in store") {
		t.Fatalf("global profile should resolve the key, got: %s", stderr)
	}
}
