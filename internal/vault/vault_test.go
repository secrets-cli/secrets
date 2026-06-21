package vault

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/vars-cli/vars/internal/crypto/sshderive"
)

// fakeCommitter records commit messages so tests can assert mutations commit.
type fakeCommitter struct{ msgs []string }

func (f *fakeCommitter) Commit(msg string) error { f.msgs = append(f.msgs, msg); return nil }

// newVault builds an initialized vault backed by a fresh Ed25519 key, loaded
// through sshderive's public file API.
func newVault(t *testing.T, c Committer) *Vault {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	signer, err := sshderive.FromFile(keyPath)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	dir := t.TempDir()
	if err := Init(dir, Meta{Scheme: "ssh-v1", KeyFingerprint: signer.Fingerprint()}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return New(dir, sshderive.NewBackend(signer), c)
}

func TestVault_SetGet(t *testing.T) {
	v := newVault(t, nil)
	if err := v.Set("RPC_URL", []byte("https://rpc.example.com")); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := v.Get("RPC_URL")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "https://rpc.example.com" {
		t.Fatalf("got %q", got)
	}
}

// Each '/'-separated key segment is restricted to [A-Za-z0-9_-]: portable, no
// Unicode-normalization collisions, no path traversal, no surprising characters.
func TestValidateKey(t *testing.T) {
	valid := []string{"RPC_URL", "prod/PRIVATE_KEY", "a/b/c", "API-KEY", "v2-key_3"}
	for _, k := range valid {
		if err := validateKey(k); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", k, err)
		}
	}
	invalid := []string{
		"café",       // accented (non-ASCII)
		"clé_privée", // accented
		"API.KEY",    // dot
		"a b",        // space
		"a@b",        // punctuation
		"a\tb",       // control character
		"a\\b",       // backslash
		"/leading",   // leading slash
		"trailing/",  // trailing slash
		"a//b",       // empty segment
		"a/../b",     // relative segment (dot rejected)
		"K~1",        // reserved version marker
		"",           // empty
	}
	for _, k := range invalid {
		if err := validateKey(k); err == nil {
			t.Errorf("validateKey(%q) = nil, want an error", k)
		}
	}
}

// A write self-heals missing static scaffolding (e.g. a deleted .gitignore that
// would otherwise leave the default-deny allowlist disarmed).
func TestSet_HealsMissingScaffold(t *testing.T) {
	v := newVault(t, nil)
	// A fresh Init writes only store.json; the static files appear on first write.
	if err := v.Set("K", []byte("v")); err != nil {
		t.Fatalf("set: %v", err)
	}
	for _, f := range []string{"README.md", ".gitignore", ".gitattributes"} {
		if _, err := os.Stat(filepath.Join(v.dir, f)); err != nil {
			t.Fatalf("write did not heal %s: %v", f, err)
		}
	}
}

// WriteScaffold restores only what's missing; it never clobbers a customized file.
func TestWriteScaffold_PreservesExisting(t *testing.T) {
	v := newVault(t, nil)
	readme := filepath.Join(v.dir, "README.md")
	if err := os.WriteFile(readme, []byte("custom"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := WriteScaffold(v.dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if b, _ := os.ReadFile(readme); string(b) != "custom" {
		t.Fatalf("clobbered a customized README: %q", b)
	}
}

// A missing key must be distinguishable (errors.Is ErrNotFound) from a real
// decrypt/IO failure, so resolve's scope fallback and import don't mask the latter.
func TestVault_GetMissingIsErrNotFound(t *testing.T) {
	v := newVault(t, nil)
	_, err := v.Get("NOPE")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want wrapping ErrNotFound", err)
	}
	if err := v.Delete("NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(missing) err = %v, want wrapping ErrNotFound", err)
	}
	if err := v.Rename("NOPE", "X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(missing) err = %v, want wrapping ErrNotFound", err)
	}
}

// SetMany must validate/encrypt everything before writing, so one bad key aborts
// the whole batch with nothing written — not even valid keys that precede it.
func TestVault_SetManyAtomicOnBadKey(t *testing.T) {
	v := newVault(t, nil)
	items := []Item{
		{Key: "GOOD1", Value: []byte("a")},
		{Key: "../escape", Value: []byte("b")}, // invalid path → abort the batch
		{Key: "GOOD2", Value: []byte("c")},
	}
	if err := v.SetMany(items, "import"); err == nil {
		t.Fatal("SetMany with an invalid key should fail")
	}
	if v.Has("GOOD1") || v.Has("GOOD2") {
		t.Fatal("SetMany must write no key when one is invalid (atomic up front)")
	}
}

func TestVault_ScopedKeysAreDirectories(t *testing.T) {
	v := newVault(t, nil)
	if err := v.Set("prod/PRIVATE_KEY", []byte("0xPROD")); err != nil {
		t.Fatalf("set: %v", err)
	}
	// The file lives at <dir>/prod/PRIVATE_KEY.age
	if _, err := os.Stat(filepath.Join(v.dir, "prod", "PRIVATE_KEY.age")); err != nil {
		t.Fatalf("expected scoped file on disk: %v", err)
	}
	got, err := v.Get("prod/PRIVATE_KEY")
	if err != nil || string(got) != "0xPROD" {
		t.Fatalf("get scoped: %q err %v", got, err)
	}
}

func TestVault_ListAndScopes(t *testing.T) {
	v := newVault(t, nil)
	for _, k := range []string{"RPC_URL", "prod/PRIVATE_KEY", "dev/temp/PRIVATE_KEY"} {
		if err := v.Set(k, []byte("x")); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	keys, err := v.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"RPC_URL", "dev/temp/PRIVATE_KEY", "prod/PRIVATE_KEY"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("list = %v, want %v", keys, want)
	}
	scopes, err := v.Scopes()
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	wantScopes := []string{"dev", "dev/temp", "prod"}
	if !reflect.DeepEqual(scopes, wantScopes) {
		t.Fatalf("scopes = %v, want %v", scopes, wantScopes)
	}
}

func TestVault_DeletePrunesEmptyDirs(t *testing.T) {
	v := newVault(t, nil)
	if err := v.Set("dev/temp/KEY", []byte("x")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := v.Delete("dev/temp/KEY"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := v.Get("dev/temp/KEY"); err == nil {
		t.Fatal("expected key gone")
	}
	// Empty dev/ and dev/temp/ should be pruned.
	if _, err := os.Stat(filepath.Join(v.dir, "dev")); !os.IsNotExist(err) {
		t.Fatalf("expected dev/ pruned, stat err = %v", err)
	}
}

func TestVault_RenameKeepsValueNoReencrypt(t *testing.T) {
	v := newVault(t, nil)
	if err := v.Set("OLD", []byte("secret")); err != nil {
		t.Fatalf("set: %v", err)
	}
	oldBytes, _ := os.ReadFile(filepath.Join(v.dir, "OLD.age"))

	if err := v.Rename("OLD", "scoped/NEW"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if v.Has("OLD") {
		t.Fatal("OLD should be gone")
	}
	got, err := v.Get("scoped/NEW")
	if err != nil || string(got) != "secret" {
		t.Fatalf("get renamed: %q err %v", got, err)
	}
	// Same ciphertext bytes moved (no re-encryption).
	newBytes, _ := os.ReadFile(filepath.Join(v.dir, "scoped", "NEW.age"))
	if string(oldBytes) != string(newBytes) {
		t.Fatal("rename should move bytes unchanged (no re-encryption)")
	}
}

func TestVault_RenameConflicts(t *testing.T) {
	v := newVault(t, nil)
	v.Set("A", []byte("a"))
	v.Set("B", []byte("b"))
	if err := v.Rename("A", "B"); err == nil {
		t.Fatal("rename onto existing key should fail")
	}
	if err := v.Rename("MISSING", "C"); err == nil {
		t.Fatal("rename of missing key should fail")
	}
}

func TestVault_GetMissing(t *testing.T) {
	v := newVault(t, nil)
	if _, err := v.Get("NOPE"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestVault_RejectsTraversalKeys(t *testing.T) {
	v := newVault(t, nil)
	for _, bad := range []string{"", "/abs", "trailing/", "../escape", "a/../b", "a//b", "nul\x00key", "ver~2"} {
		if err := v.Set(bad, []byte("x")); err == nil {
			t.Fatalf("Set(%q) should have been rejected", bad)
		}
	}
	// And nothing escaped the store dir.
	parent := filepath.Dir(v.dir)
	if _, err := os.Stat(filepath.Join(parent, "escape.age")); !os.IsNotExist(err) {
		t.Fatal("a traversal key wrote outside the store")
	}
}

func TestVault_CommitsOnMutation(t *testing.T) {
	fc := &fakeCommitter{}
	v := newVault(t, fc)
	v.Set("K", []byte("1"))
	v.Set("scope/K2", []byte("2"))
	v.Rename("K", "K3")
	v.Delete("scope/K2")

	want := []string{"set K", "set scope/K2", "mv K K3", "rm scope/K2"}
	if !reflect.DeepEqual(fc.msgs, want) {
		t.Fatalf("commit messages = %v, want %v", fc.msgs, want)
	}
}

func TestVault_SetManyCommitsOnce(t *testing.T) {
	fc := &fakeCommitter{}
	v := newVault(t, fc)
	items := []Item{{"A", []byte("1")}, {"scope/B", []byte("2")}, {"C", []byte("3")}}
	if err := v.SetMany(items, "import 3 keys"); err != nil {
		t.Fatalf("setmany: %v", err)
	}
	if len(fc.msgs) != 1 || fc.msgs[0] != "import 3 keys" {
		t.Fatalf("expected one commit %q, got %v", "import 3 keys", fc.msgs)
	}
	for _, it := range items {
		got, err := v.Get(it.Key)
		if err != nil || string(got) != string(it.Value) {
			t.Fatalf("get %s: %q err %v", it.Key, got, err)
		}
	}
}

func TestVault_DeleteManyCommitsOnce(t *testing.T) {
	fc := &fakeCommitter{}
	v := newVault(t, fc)
	v.SetMany([]Item{{"A", []byte("1")}, {"B", []byte("2")}}, "seed")
	fc.msgs = nil
	if err := v.DeleteMany([]string{"A", "B"}, "rm 2 keys"); err != nil {
		t.Fatalf("deletemany: %v", err)
	}
	if len(fc.msgs) != 1 || fc.msgs[0] != "rm 2 keys" {
		t.Fatalf("expected one commit, got %v", fc.msgs)
	}
	if v.Has("A") || v.Has("B") {
		t.Fatal("keys should be gone")
	}
}

func TestVault_GetVersionWithoutHistory(t *testing.T) {
	// nil committer → no history.
	if _, err := newVault(t, nil).GetVersion("K", 1); err == nil {
		t.Fatal("GetVersion without a committer should error")
	}
	// a Committer that isn't a History → still no version access.
	if _, err := newVault(t, &fakeCommitter{}).GetVersion("K", 1); err == nil {
		t.Fatal("GetVersion with a non-History committer should error")
	}
}

func TestVault_InitAndMeta(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Fatal("fresh dir should not be an existing store")
	}
	if err := Init(dir, Meta{Scheme: "ssh-v1", KeyFingerprint: "SHA256:abc"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !Exists(dir) {
		t.Fatal("store should exist after init")
	}
	if err := Init(dir, Meta{Scheme: "ssh-v1"}); err == nil {
		t.Fatal("re-init should fail")
	}
	m, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if m.Scheme != "ssh-v1" || m.KeyFingerprint != "SHA256:abc" {
		t.Fatalf("meta = %+v", m)
	}
}
