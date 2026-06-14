// Package session opens and creates the vars store. It is the single place
// command code goes from "a directory" to "a usable *vault.Vault": it resolves
// the user's SSH key (the ssh-v1 backend) and wires git versioning.
package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vars-cli/vars/internal/crypto/sshderive"
	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/vault"
)

// Scheme is the only store scheme this build understands.
const Scheme = "ssh-v1"

// Open returns a ready vault for an existing store at dir, resolving the SSH
// key it requires and attaching git versioning when dir is a repo.
func Open(dir string) (*vault.Vault, error) {
	if !vault.Exists(dir) {
		return nil, fmt.Errorf("no vars store at %s — run `vars` to create one", dir)
	}
	signer, err := ResolveSigner(dir)
	if err != nil {
		return nil, err
	}
	return vaultWith(dir, signer), nil
}

// vaultWith builds a vault, attaching a git Committer only when dir is a repo.
func vaultWith(dir string, signer *sshderive.Signer) *vault.Vault {
	var committer vault.Committer
	if git.Available() && git.IsRepo(dir) {
		committer = git.New(dir)
	}
	return vault.New(dir, sshderive.NewBackend(signer), committer)
}

// ResolveSigner finds the key an existing store needs, by its recorded fingerprint.
func ResolveSigner(dir string) (*sshderive.Signer, error) {
	meta, err := vault.ReadMeta(dir)
	if err != nil {
		return nil, fmt.Errorf("reading store metadata: %w", err)
	}
	if meta.Scheme != Scheme {
		return nil, fmt.Errorf("unsupported store scheme %q (this vars supports %q)", meta.Scheme, Scheme)
	}
	return signerForFingerprint(meta.KeyFingerprint)
}

// signerForFingerprint resolves a signer matching fp: VARS_SSH_KEY file, then
// ssh-agent, then the default key file.
func signerForFingerprint(fp string) (*sshderive.Signer, error) {
	if path := os.Getenv("VARS_SSH_KEY"); path != "" {
		s, err := sshderive.FromFile(path, nil)
		if err != nil {
			return nil, fmt.Errorf("VARS_SSH_KEY %s: %w", path, err)
		}
		if fp != "" && s.Fingerprint() != fp {
			return nil, fmt.Errorf("VARS_SSH_KEY key %s does not match the store key %s", s.Fingerprint(), fp)
		}
		return s, nil
	}
	if ag, conn, err := sshderive.DialAgent(); err == nil {
		defer conn.Close()
		if s, err := sshderive.FromAgent(ag, fp); err == nil {
			return s, nil
		}
	}
	if path := defaultKeyPath(); path != "" {
		if s, err := sshderive.FromFile(path, nil); err == nil && (fp == "" || s.Fingerprint() == fp) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("could not find the SSH key this store needs (%s); load it with `ssh-add`, or point VARS_SSH_KEY at its file", fp)
}

// UsableInitSigners returns candidate keys for creating a new store: the
// VARS_SSH_KEY file if set, otherwise every usable key in ssh-agent, otherwise
// the default key file. Callers pick one (auto when there is exactly one).
func UsableInitSigners() ([]*sshderive.Signer, error) {
	if path := os.Getenv("VARS_SSH_KEY"); path != "" {
		s, err := sshderive.FromFile(path, nil)
		if err != nil {
			return nil, fmt.Errorf("VARS_SSH_KEY %s: %w", path, err)
		}
		return []*sshderive.Signer{s}, nil
	}
	if ag, conn, err := sshderive.DialAgent(); err == nil {
		defer conn.Close()
		if signers, err := sshderive.AgentSigners(ag); err == nil && len(signers) > 0 {
			return signers, nil
		}
	}
	if path := defaultKeyPath(); path != "" {
		if s, err := sshderive.FromFile(path, nil); err == nil {
			return []*sshderive.Signer{s}, nil
		}
	}
	return nil, fmt.Errorf("no usable SSH key found; create one with `ssh-keygen -t ed25519` and load it with `ssh-add`")
}

// Create initializes a new store at dir for the given key: writes vault.json and
// RECOVERY.md, and (when git is available) inits a repo with an initial commit.
func Create(dir string, signer *sshderive.Signer) error {
	if err := vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: signer.Fingerprint()}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "RECOVERY.md"), []byte(recoveryDoc), 0o644); err != nil {
		return fmt.Errorf("writing RECOVERY.md: %w", err)
	}
	if git.Available() {
		repo := git.New(dir)
		if err := repo.Init(); err != nil {
			return fmt.Errorf("initializing git: %w", err)
		}
		if err := repo.Commit("init vars store"); err != nil {
			return fmt.Errorf("initial commit: %w", err)
		}
	}
	return nil
}

// defaultKeyPath returns the first existing default SSH key file, or "".
func defaultKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// recoveryDoc is written to RECOVERY.md so a store can be decrypted even
// without the vars binary, using only standard tools plus a small script.
const recoveryDoc = "# Recovering secrets from this store\n\n" +
	"Each `*.age` file here is encrypted with [age](https://age-encryption.org), but its\n" +
	"file key is wrapped by a key derived from your SSH key (scheme `ssh-v1`, recorded in\n" +
	"`vault.json`). The plain `age` CLI therefore cannot open these files by itself.\n\n" +
	"## Easy path\n\n" +
	"If the `vars` binary works:\n\n" +
	"    vars dump          # print every key and value\n\n" +
	"## Break-glass (no vars binary)\n\n" +
	"You need the SSH private key whose fingerprint is in `vault.json` (`key_fingerprint`).\n" +
	"For one file `<key>.age`:\n\n" +
	"1. In its age header, find the `-> vars-ssh-v1 <salt>` stanza. `<salt>` is base64\n" +
	"   (raw std). The stanza body is `nonce (12 bytes) || ChaCha20-Poly1305-sealed file key`.\n" +
	"2. Sign the decoded salt with namespace `vars.store.v1` (SSHSIG, the default sha512):\n\n" +
	"       ssh-keygen -Y sign -n vars.store.v1 -f ~/.ssh/id_ed25519 salt-file\n\n" +
	"   The `.sig` is SSHSIG armor; its inner signature field is\n" +
	"   `string(sig-format) || string(sig-blob)` — call those bytes WIRE.\n" +
	"3. `wrapKey = HKDF-SHA256(secret=WIRE, salt=<decoded salt>, info=\"vars.store.v1/fileKey\", len=32)`\n" +
	"4. `fileKey = ChaCha20-Poly1305-Open(key=wrapKey, nonce=body[:12], ciphertext=body[12:])`\n" +
	"5. Decrypt the file with that file key, e.g. in Go:\n" +
	"   `age.Decrypt(file, age.NewInjectedFileKeyIdentity(fileKey))`.\n\n" +
	"The authoritative reference for steps 1–5 is `internal/crypto/sshderive` in the\n" +
	"vars source. `ssh-keygen -Y sign -n vars.store.v1` reproduces step 2 exactly.\n"
