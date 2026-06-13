package sshderive

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// signerFromKey builds an in-process Signer from any crypto private key.
func signerFromKey(t *testing.T, priv any) *Signer {
	t.Helper()
	ss, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	s, err := fromSSHSigner(ss)
	if err != nil {
		t.Fatalf("fromSSHSigner: %v", err)
	}
	return s
}

func newEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	return priv
}

func TestBackend_RoundTrip(t *testing.T) {
	b := NewBackend(signerFromKey(t, newEd25519(t)))
	plain := []byte("0xDEADBEEF super secret value\nwith a second line")

	ct, err := b.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := b.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
	}
}

// The whole scheme rests on the agent and file paths deriving the SAME key from
// the same SSH key. Encrypt via the file signer, decrypt via an ssh-agent
// signer for the same key.
func TestBackend_AgentAndFileAgree(t *testing.T) {
	priv := newEd25519(t)
	fileBackend := NewBackend(signerFromKey(t, priv))

	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatalf("agent add: %v", err)
	}
	ss, _ := ssh.NewSignerFromKey(priv)
	fp := ssh.FingerprintSHA256(ss.PublicKey())
	agentSigner, err := FromAgent(kr, fp)
	if err != nil {
		t.Fatalf("FromAgent: %v", err)
	}
	agentBackend := NewBackend(agentSigner)

	plain := []byte("cross-path value")
	ct, err := fileBackend.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt (file): %v", err)
	}
	got, err := agentBackend.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt (agent) of file-encrypted ciphertext: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("agent/file disagree: got %q want %q", got, plain)
	}
}

// Each encryption uses a fresh random salt, so identical plaintext yields
// different ciphertext, yet both decrypt.
func TestBackend_UniqueSaltPerEncrypt(t *testing.T) {
	b := NewBackend(signerFromKey(t, newEd25519(t)))
	plain := []byte("same input")

	ct1, _ := b.Encrypt(plain)
	ct2, _ := b.Encrypt(plain)
	if bytes.Equal(ct1, ct2) {
		t.Fatal("expected distinct ciphertexts (random salt per file)")
	}
	for _, ct := range [][]byte{ct1, ct2} {
		got, err := b.Decrypt(ct)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("decrypt: got %q err %v", got, err)
		}
	}
}

func TestBackend_WrongKeyFails(t *testing.T) {
	enc := NewBackend(signerFromKey(t, newEd25519(t)))
	other := NewBackend(signerFromKey(t, newEd25519(t)))

	ct, err := enc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := other.Decrypt(ct); err == nil {
		t.Fatal("decrypt with a different SSH key should fail")
	}
}

func TestBackend_TamperDetected(t *testing.T) {
	b := NewBackend(signerFromKey(t, newEd25519(t)))
	ct, err := b.Encrypt([]byte("authentic"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct[len(ct)-1] ^= 0xFF // flip a byte in the payload
	if _, err := b.Decrypt(ct); err == nil {
		t.Fatal("tampered ciphertext should fail authentication")
	}
}

func TestBackend_RSARoundTrip(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	b := NewBackend(signerFromKey(t, priv))
	plain := []byte("rsa secret")
	ct, err := b.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := b.Decrypt(ct)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("rsa round-trip: got %q err %v", got, err)
	}
}

func TestSigner_RejectsECDSA(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa keygen: %v", err)
	}
	ss, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	if _, err := fromSSHSigner(ss); err == nil {
		t.Fatal("ECDSA key should be rejected (non-deterministic)")
	}
}

func TestFromFile_RoundTripAndType(t *testing.T) {
	priv := newEd25519(t)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	s, err := FromFile(keyPath, nil)
	if err != nil {
		t.Fatalf("FromFile: %v", err)
	}
	ss, _ := ssh.NewSignerFromKey(priv)
	if s.Fingerprint() != ssh.FingerprintSHA256(ss.PublicKey()) {
		t.Fatalf("fingerprint mismatch: %s", s.Fingerprint())
	}

	// And the file-loaded signer agrees with the in-memory one.
	b := NewBackend(s)
	ct, _ := NewBackend(signerFromKey(t, priv)).Encrypt([]byte("x"))
	if got, err := b.Decrypt(ct); err != nil || string(got) != "x" {
		t.Fatalf("file signer disagrees: got %q err %v", got, err)
	}
}

// The break-glass guarantee: our SSHSIG signature must be byte-for-byte what
// `ssh-keygen -Y sign -n vars.store.v1` produces, so a store can be recovered
// with standard tools alone.
func TestSSHSIG_MatchesSshKeygen(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	priv := newEd25519(t)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	ss, _ := ssh.NewSignerFromKey(priv)
	if err := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(ss.PublicKey()), 0o644); err != nil {
		t.Fatalf("write pub: %v", err)
	}

	salt := bytes.Repeat([]byte{0xA5}, saltLen)
	msgPath := filepath.Join(dir, "msg")
	if err := os.WriteFile(msgPath, salt, 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", keyPath, "-n", namespace, msgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v\n%s", err, out)
	}
	armored, err := os.ReadFile(msgPath + ".sig")
	if err != nil {
		t.Fatalf("read .sig: %v", err)
	}

	// Our wire signature for the same salt.
	s := signerFromKey(t, priv)
	sig, err := s.signRaw(sshsigSignedData(namespace, salt))
	if err != nil {
		t.Fatalf("signRaw: %v", err)
	}
	ours := wireSignature(sig)

	keygen := extractSSHSIGSignature(t, armored)
	if !bytes.Equal(ours, keygen) {
		t.Fatalf("SSHSIG signature mismatch vs ssh-keygen:\n ours:   %x\n keygen: %x", ours, keygen)
	}
}

// extractSSHSIGSignature parses an armored SSHSIG (.sig) file and returns its
// inner signature field (string(format) || string(blob)) — the bytes we HKDF.
func extractSSHSIGSignature(t *testing.T, armored []byte) []byte {
	t.Helper()
	s := string(armored)
	s = strings.ReplaceAll(s, "-----BEGIN SSH SIGNATURE-----", "")
	s = strings.ReplaceAll(s, "-----END SSH SIGNATURE-----", "")
	s = strings.Join(strings.Fields(s), "") // drop all whitespace/newlines
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode armor: %v", err)
	}
	if string(raw[:6]) != "SSHSIG" {
		t.Fatalf("bad SSHSIG magic")
	}
	p := raw[6:]
	p = p[4:] // skip uint32 version
	readStr := func() []byte {
		n := binary.BigEndian.Uint32(p[:4])
		p = p[4:]
		v := p[:n]
		p = p[n:]
		return v
	}
	readStr() // publickey
	readStr() // namespace
	readStr() // reserved
	readStr() // hash_algorithm
	return readStr() // signature
}

func TestFromAgent_FingerprintSelection(t *testing.T) {
	priv := newEd25519(t)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatalf("agent add: %v", err)
	}
	ss, _ := ssh.NewSignerFromKey(priv)
	fp := ssh.FingerprintSHA256(ss.PublicKey())

	if _, err := FromAgent(kr, fp); err != nil {
		t.Fatalf("FromAgent with correct fingerprint: %v", err)
	}
	if _, err := FromAgent(kr, "SHA256:bogusbogusbogusbogusbogusbogusbogusbogus"); err == nil {
		t.Fatal("FromAgent with wrong fingerprint should error")
	}
}
