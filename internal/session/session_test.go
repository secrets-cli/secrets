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

func TestResolveSigner_ViaEnvKey(t *testing.T) {
	keyPath, fp := writeKey(t)
	dir := t.TempDir()
	if err := vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: fp}); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Setenv("VARS_SSH_KEY", keyPath)

	s, err := ResolveSigner(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s.Fingerprint() != fp {
		t.Fatalf("fingerprint = %s, want %s", s.Fingerprint(), fp)
	}
}

func TestResolveSigner_FingerprintMismatch(t *testing.T) {
	_, fpA := writeKey(t)      // the store's key
	keyB, _ := writeKey(t)     // a different key
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: fpA})
	t.Setenv("VARS_SSH_KEY", keyB)

	if _, err := ResolveSigner(dir); err == nil {
		t.Fatal("expected a fingerprint-mismatch error")
	}
}

func TestResolveSigner_UnsupportedScheme(t *testing.T) {
	keyPath, fp := writeKey(t)
	dir := t.TempDir()
	vault.Init(dir, vault.Meta{Scheme: "ssh-v999", KeyFingerprint: fp})
	t.Setenv("VARS_SSH_KEY", keyPath)
	if _, err := ResolveSigner(dir); err == nil {
		t.Fatal("expected unsupported-scheme error")
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
	for _, want := range []string{"!*/", "!*.age", "!/store.json", "!/README.md", "!/.gitignore"} {
		if !strings.Contains(string(gi), want) {
			t.Fatalf(".gitignore missing %q", want)
		}
	}
}
