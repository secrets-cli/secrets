package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/vars-cli/vars/internal/vault"
)

// writeKey generates an Ed25519 key, writes it as an OpenSSH private key file,
// and returns its path and SHA256 fingerprint.
func writeKey(t *testing.T) (path, fingerprint string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	ss, _ := ssh.NewSignerFromKey(priv)
	return path, ssh.FingerprintSHA256(ss.PublicKey())
}

// noKeyEnv neutralizes every SSH key source (env override, agent, default file)
// so the resolver finds nothing: the "no key available" edge.
func noKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("VARS_SSH_KEY", "")
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir()) // a home with no ~/.ssh keys
}

// With no key anywhere, creating a store (first run) errors with the actionable
// ssh-keygen hint instead of half-creating something it can't encrypt to.
func TestUsableInitSigners_NoKey(t *testing.T) {
	noKeyEnv(t)
	_, err := UsableInitSigners()
	if err == nil {
		t.Fatal("expected an error when no SSH key is available")
	}
	if !strings.Contains(err.Error(), "ssh-keygen") {
		t.Fatalf("error should hint at ssh-keygen, got %q", err)
	}
}

// With no key anywhere, resolving the store's key (read/write path) errors with
// the actionable ssh-add hint.
func TestSignerForFingerprint_NoKey(t *testing.T) {
	noKeyEnv(t)
	_, err := signerForFingerprint("SHA256:irrelevant")
	if err == nil {
		t.Fatal("expected an error when no SSH key is available")
	}
	if !strings.Contains(err.Error(), "ssh-add") {
		t.Fatalf("error should hint at ssh-add, got %q", err)
	}
}

func TestSignerForFingerprint_ViaEnvKey(t *testing.T) {
	keyPath, fp := writeKey(t)
	t.Setenv("VARS_SSH_KEY", keyPath)
	s, err := signerForFingerprint(fp)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s.Fingerprint() != fp {
		t.Fatalf("fingerprint = %s, want %s", s.Fingerprint(), fp)
	}
}

func TestOpen_UnsupportedScheme(t *testing.T) {
	keyPath, fp := writeKey(t)
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: "ssh-v999", KeyFingerprint: fp})
	t.Setenv("VARS_SSH_KEY", keyPath)
	if _, err := Open(dir); err == nil {
		t.Fatal("Open should reject an unsupported scheme (no key needed for this check)")
	}
}

// Open resolves the key lazily: metadata operations work without it; only
// encrypt/decrypt require it. Guards against the regression where `vars ls`
// demanded ssh-add despite decrypting nothing.
func TestOpen_LazyKey(t *testing.T) {
	keyPath, fp := writeKey(t)
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: fp})

	t.Setenv("VARS_SSH_KEY", keyPath)
	v, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := v.Set("K", []byte("v")); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Reopen with no usable key.
	t.Setenv("VARS_SSH_KEY", filepath.Join(t.TempDir(), "absent"))
	v2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open must not require the key: %v", err)
	}
	if keys, err := v2.List(); err != nil || len(keys) != 1 {
		t.Fatalf("List should work without the key: keys=%v err=%v", keys, err)
	}
	if _, err := v2.Get("K"); err == nil {
		t.Fatal("Get should fail when no usable key is available")
	}
}

func TestOpen_FingerprintMismatchSurfacesOnUse(t *testing.T) {
	_, fpA := writeKey(t)  // the store's key
	keyB, _ := writeKey(t) // a different, valid key
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: fpA})
	t.Setenv("VARS_SSH_KEY", keyB)

	v, err := Open(dir) // lazy: Open succeeds even though the key won't match
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := v.Set("K", []byte("v")); err == nil {
		t.Fatal("Set should fail: the available key does not match the store fingerprint")
	}
}

func TestOpen_ReadWriteRoundTrip(t *testing.T) {
	keyPath, fp := writeKey(t)
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: fp})
	t.Setenv("VARS_SSH_KEY", keyPath)

	v, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := v.Set("RPC_URL", []byte("https://rpc")); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := v.Get("RPC_URL")
	if err != nil || string(got) != "https://rpc" {
		t.Fatalf("get: %q err %v", got, err)
	}
}

func TestOpen_NoStore(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open on a non-store dir should error")
	}
}

func TestUsableInitSigners_ViaEnv(t *testing.T) {
	keyPath, fp := writeKey(t)
	t.Setenv("VARS_SSH_KEY", keyPath)
	signers, err := UsableInitSigners()
	if err != nil {
		t.Fatalf("usable: %v", err)
	}
	if len(signers) != 1 || signers[0].Fingerprint() != fp {
		t.Fatalf("expected the single env key, got %d signers", len(signers))
	}
}

func TestCreate_WritesMetaAndRecovery(t *testing.T) {
	keyPath, fp := writeKey(t)
	t.Setenv("VARS_SSH_KEY", keyPath)
	signers, err := UsableInitSigners()
	if err != nil {
		t.Fatalf("usable: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "store")
	if err := Create(dir, signers[0]); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !vault.Exists(dir) {
		t.Fatal("store should exist after Create")
	}
	meta, err := vault.ReadMeta(dir)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta.Scheme != Scheme || meta.KeyFingerprint != fp {
		t.Fatalf("meta = %+v", meta)
	}
	rec, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, want := range []string{"ssh-keygen -Y sign", "vars.store.v1", "HKDF-SHA256", "vars dump"} {
		if !strings.Contains(string(rec), want) {
			t.Fatalf("README.md missing %q", want)
		}
	}

	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{"!*/", "!*.age", "!/store.json", "!/README.md", "!/.gitignore", "!/.gitattributes"} {
		if !strings.Contains(string(gi), want) {
			t.Fatalf(".gitignore missing %q", want)
		}
	}

	ga, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if !strings.Contains(string(ga), "*.age binary") {
		t.Fatalf(".gitattributes missing `*.age binary`, got %q", ga)
	}
}
