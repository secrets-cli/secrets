// Package session opens and creates the vars store. It is the single place
// command code goes from "a directory" to "a usable *vault.Vault": it resolves
// the user's SSH key (the ssh-v1 backend) and wires git versioning.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/vars-cli/vars/internal/crypto"
	"github.com/vars-cli/vars/internal/crypto/sshderive"
	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/vault"
)

// Scheme is the only store scheme this build understands.
const Scheme = "ssh-v1"

// Open returns a ready vault for an existing store at dir, attaching git
// versioning when dir is a repo. The SSH key is resolved lazily (see
// lazyBackend), so key-free commands (ls, scope, mv, rm) never require it; the
// key is demanded only when a command actually encrypts or decrypts.
func Open(dir string) (*vault.Vault, error) {
	if !vault.Exists(dir) {
		return nil, fmt.Errorf("no vars store at %s: run `vars` to create one", dir)
	}
	meta, err := loadMeta(dir) // cheap, no key: validates the store is one we understand
	if err != nil {
		return nil, err
	}
	var committer vault.Committer
	if git.Available() && git.IsRepo(dir) {
		committer = gitCommitter{git.New(dir)}
	}
	return vault.New(dir, newLazyBackend(meta.KeyFingerprint), committer), nil
}

// loadMeta reads the store descriptor and verifies this build understands it.
func loadMeta(dir string) (vault.Meta, error) {
	meta, err := vault.ReadMeta(dir)
	if err != nil {
		return meta, fmt.Errorf("reading store metadata: %w", err)
	}
	if meta.Scheme != Scheme {
		return meta, fmt.Errorf("unsupported store scheme %q (this vars supports %q)", meta.Scheme, Scheme)
	}
	return meta, nil
}

// lazyBackend defers SSH key resolution until the first encrypt/decrypt, so a
// command that only reads or moves files (ls, scope, mv, rm) never touches the
// key. The key (matched to the store's fingerprint) is resolved at most once.
type lazyBackend struct {
	fingerprint string
	once        sync.Once
	backend     crypto.Backend
	err         error
}

var _ crypto.Backend = (*lazyBackend)(nil)

func newLazyBackend(fingerprint string) *lazyBackend { return &lazyBackend{fingerprint: fingerprint} }

func (l *lazyBackend) resolve() (crypto.Backend, error) {
	l.once.Do(func() {
		signer, err := signerForFingerprint(l.fingerprint)
		if err != nil {
			l.err = err
			return
		}
		l.backend = sshderive.NewBackend(signer)
	})
	return l.backend, l.err
}

func (l *lazyBackend) Encrypt(plaintext []byte) ([]byte, error) {
	b, err := l.resolve()
	if err != nil {
		return nil, err
	}
	return b.Encrypt(plaintext)
}

func (l *lazyBackend) Decrypt(ciphertext []byte) ([]byte, error) {
	b, err := l.resolve()
	if err != nil {
		return nil, err
	}
	return b.Decrypt(ciphertext)
}

// gitCommitter adapts *git.Repo to vault.Committer. git is a soft dependency:
// a commit failure must never fail the mutation (the secret is already written),
// so it degrades to a warning and the store is simply left un-versioned.
type gitCommitter struct{ repo *git.Repo }

func (g gitCommitter) Commit(message string) error {
	if err := g.repo.Commit(message); err != nil {
		fmt.Fprintf(os.Stderr, "vars: warning: saved, but git did not version it (%v).\n"+
			"  Run `vars sync` to commit and push it; until then it folds into the next commit.\n", err)
	}
	return nil
}

// VersionContent satisfies vault.History, enabling `vars get KEY~N`.
func (g gitCommitter) VersionContent(relpath string, n int) ([]byte, error) {
	return g.repo.VersionContent(relpath, n)
}

// signerForFingerprint resolves a signer matching fp: VARS_SSH_KEY file, then
// ssh-agent, then the default key file.
func signerForFingerprint(fp string) (*sshderive.Signer, error) {
	if path := os.Getenv("VARS_SSH_KEY"); path != "" {
		s, err := sshderive.FromFile(path)
		if err != nil {
			return nil, fmt.Errorf("VARS_SSH_KEY %s: %w", path, err)
		}
		if fp != "" && s.Fingerprint() != fp {
			return nil, fmt.Errorf("VARS_SSH_KEY key %s does not match the store key %s", s.Fingerprint(), fp)
		}
		return s, nil
	}
	if ag, conn, err := sshderive.DialAgent(); err == nil {
		if s, err := sshderive.FromAgent(ag, fp); err == nil {
			// Keep the connection open: the signer uses it for every encrypt/
			// decrypt this command performs. The process reclaims it on exit.
			return s, nil
		}
		conn.Close() // agent lacks the key — release before trying the key file
	}
	if path := defaultKeyPath(); path != "" {
		if s, err := sshderive.FromFile(path); err == nil && (fp == "" || s.Fingerprint() == fp) {
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
		s, err := sshderive.FromFile(path)
		if err != nil {
			return nil, fmt.Errorf("VARS_SSH_KEY %s: %w", path, err)
		}
		return []*sshderive.Signer{s}, nil
	}
	if ag, conn, err := sshderive.DialAgent(); err == nil {
		if signers, err := sshderive.AgentSigners(ag); err == nil && len(signers) > 0 {
			// Keep the connection open: returned signers use it until exit.
			return signers, nil
		}
		conn.Close()
	}
	if path := defaultKeyPath(); path != "" {
		if s, err := sshderive.FromFile(path); err == nil {
			return []*sshderive.Signer{s}, nil
		}
	}
	return nil, fmt.Errorf("no usable SSH key found; create one with `ssh-keygen -t ed25519` and load it with `ssh-add`")
}

// Create initializes a new store at dir for the given key: writes store.json and
// README.md, and (when git is available) inits a repo with an initial commit.
func Create(dir string, signer *sshderive.Signer) error {
	if err := vault.Init(dir, vault.Meta{Scheme: Scheme, KeyFingerprint: signer.Fingerprint()}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(storeReadme), 0o644); err != nil {
		return fmt.Errorf("writing README.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(storeGitignore), 0o644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(storeGitattributes), 0o644); err != nil {
		return fmt.Errorf("writing .gitattributes: %w", err)
	}
	// git is a soft dependency: if it's missing or fails, the store is still
	// created and fully usable, just without versioning/sync.
	if git.Available() {
		repo := git.New(dir)
		if err := repo.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "vars: warning: git init failed (store created, not versioned): %v\n", err)
		} else if err := repo.Commit("init vars store"); err != nil {
			fmt.Fprintf(os.Stderr, "vars: warning: initial git commit failed: %v\n", err)
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

// storeGitignore is a default-deny allowlist: the store's git repo tracks only
// encrypted secrets, the descriptor, and the README — never stray plaintext,
// editor junk, or the atomic-write temp files. Users can `git add -f` to override.
const storeGitignore = `# vars store: commit only encrypted secrets, the descriptor, and this README.
*
!*/
!*.age
!/store.json
!/README.md
!/.gitignore
!/.gitattributes
`

// storeGitattributes marks encrypted files as binary so git never text-merges
// them (which would inject conflict markers into ciphertext) and never applies
// line-ending conversion (which would corrupt the bytes under core.autocrlf). A
// conflict on a key then resolves cleanly as a whole-file "pick a side".
const storeGitattributes = "*.age binary\n"

// storeReadme is written as README.md at the store root, so the directory
// explains itself and documents how to recover secrets without the vars binary.
const storeReadme = "# vars store\n\n" +
	"This directory is an encrypted [vars](https://github.com/vars-cli/vars) store: one\n" +
	"[age](https://age-encryption.org)-encrypted file per secret, each file's key derived\n" +
	"from an SSH key (scheme `ssh-v1`; the key's fingerprint is in `store.json`).\n\n" +
	"## Reading these secrets\n\n" +
	"Use vars, it's open-source and a single static Go binary, so rebuild it if needed:\n\n" +
	"    go install github.com/vars-cli/vars@latest   # if you don't have the binary\n" +
	"    vars dump                                      # print every key and value\n" +
	"    vars get <KEY>                                 # one value\n\n" +
	"## If vars is unavailable: the format\n\n" +
	"You need the SSH private key whose fingerprint is in `store.json`. Decryption uses\n" +
	"standard primitives, so it can be reimplemented in any language with a crypto library\n" +
	"(it is NOT a sequence of shell commands). For each `<key>.age`:\n\n" +
	"1. Parse the age header; find the `-> vars-ssh-v1 <salt>` stanza. `<salt>` is base64\n" +
	"   (raw std). Its body is `nonce (12 bytes) || ChaCha20-Poly1305-sealed file-key`.\n" +
	"2. Sign the decoded salt with SSHSIG, namespace `vars.store.v1` (hash sha512):\n\n" +
	"       ssh-keygen -Y sign -n vars.store.v1 -f ~/.ssh/id_ed25519 salt-file\n\n" +
	"   Use the inner signature bytes (`string(format) || string(blob)`) from the `.sig`.\n" +
	"3. `wrapKey = HKDF-SHA256(secret = signature bytes, salt = decoded salt, info = \"vars.store.v1/fileKey\")` (32 bytes).\n" +
	"4. `file-key = ChaCha20-Poly1305-Open(wrapKey, nonce, sealed)`.\n" +
	"5. Decrypt the age payload with that file-key (age's injected-file-key identity).\n\n" +
	"Reference implementation: `internal/crypto/sshderive` in the vars source.\n"
