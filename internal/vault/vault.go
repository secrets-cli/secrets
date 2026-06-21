// Package vault is the vars store: one age-encrypted file per secret, scopes as
// directories, rooted at a single directory that is usually a git repo.
//
// It does encrypted file CRUD plus the store's on-disk scaffolding (the
// store.json descriptor and the static README/.gitignore/.gitattributes).
// Versioning is delegated to an optional Committer (implemented by the git
// package), so the vault knows nothing about git and stays trivially testable.
package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vars-cli/vars/internal/crypto"
)

const (
	// DescriptorFile is the unencrypted store descriptor, committed with the store.
	DescriptorFile = "store.json"
	ageExt         = ".age"

	// lockFile is the advisory mutation lock (gitignored by the default-deny
	// allowlist, so it is never committed).
	lockFile = ".vars.lock"

	dirPerm  = 0o700
	filePerm = 0o600
)

// ErrNotFound is wrapped by Get/GetVersion/Delete/Rename when a key's file is
// absent, so callers can distinguish "missing" (e.g. scope fallback) from a real
// decryption/IO failure (which must not be masked as not-found).
var ErrNotFound = errors.New("not found in store")

// Meta describes how to open a store. It is unencrypted and committed.
type Meta struct {
	Scheme         string `json:"scheme"`          // e.g. "ssh-v1"
	KeyFingerprint string `json:"key_fingerprint"` // SHA256:… — which SSH key the store needs
}

// Committer is the optional VCS hook the vault calls after each successful
// mutation. A nil Committer disables versioning (the store still works).
type Committer interface {
	Commit(message string) error
}

// History optionally lets the vault read past versions of a key. A Committer
// that also implements History enables `GetVersion` (git-backed).
type History interface {
	// VersionContent returns the raw stored bytes of relpath n commits back
	// (n=0 current, n=1 previous, …).
	VersionContent(relpath string, n int) ([]byte, error)
}

// Vault is a per-file encrypted store rooted at dir.
type Vault struct {
	dir       string
	backend   crypto.Backend
	committer Committer // may be nil
}

// New returns a Vault over an existing store directory.
func New(dir string, backend crypto.Backend, committer Committer) *Vault {
	return &Vault{dir: dir, backend: backend, committer: committer}
}

// DefaultDir returns the default store directory.
// Priority: VARS_STORE_DIR > $XDG_DATA_HOME/vars/store > ~/.local/share/vars/store
func DefaultDir() string {
	if d := os.Getenv("VARS_STORE_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "vars", "store")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".local", "share", "vars", "store")
	}
	return filepath.Join(home, ".local", "share", "vars", "store")
}

// Exists reports whether a store has been initialized at dir.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, DescriptorFile))
	return err == nil
}

// ReadMeta reads the store descriptor.
func ReadMeta(dir string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(dir, DescriptorFile))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parsing %s: %w", DescriptorFile, err)
	}
	return m, nil
}

// Init creates the store directory and writes store.json. It errors if a store
// already exists. git initialization is the caller's concern.
func Init(dir string, meta Meta) error {
	if Exists(dir) {
		return fmt.Errorf("store already exists at %s", dir)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating store directory: %w", err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil { // MkdirAll respects umask
		return fmt.Errorf("setting directory permissions: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, DescriptorFile), append(data, '\n'), filePerm)
}

// Get decrypts and returns the value for key.
func (v *Vault) Get(key string) ([]byte, error) {
	path, err := v.pathFor(key)
	if err != nil {
		return nil, err
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("key %q %w", key, ErrNotFound)
		}
		return nil, err
	}
	return v.backend.Decrypt(ciphertext)
}

// GetVersion returns the value of key as of n versions ago (n=1 = previous,
// n=0 = current), read from git history and decrypted with the current key.
// Requires the committer to also implement History.
func (v *Vault) GetVersion(key string, n int) ([]byte, error) {
	h, ok := v.committer.(History)
	if !ok {
		return nil, fmt.Errorf("version history is unavailable (store is not git-backed)")
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	ciphertext, err := h.VersionContent(key+ageExt, n)
	if err != nil {
		return nil, err
	}
	return v.backend.Decrypt(ciphertext)
}

// Has reports whether key exists, without decrypting.
func (v *Vault) Has(key string) bool {
	path, err := v.pathFor(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Item is a key/value pair for batch writes.
type Item struct {
	Key   string
	Value []byte
}

// Set encrypts value and writes it to <key>.age, then commits.
func (v *Vault) Set(key string, value []byte) error {
	unlock, err := lockStore(v.dir)
	if err != nil {
		return err
	}
	defer unlock()
	if err := v.writeKey(key, value); err != nil {
		return err
	}
	return v.commit("set " + key)
}

// SetMany writes several keys and commits once with the given message. It
// validates and encrypts every item up front, so a bad key or encryption
// failure aborts before anything is written (no half-applied import). The only
// residual non-atomicity is a disk error partway through the final write loop,
// which no single-rename scheme can avoid.
func (v *Vault) SetMany(items []Item, message string) error {
	unlock, err := lockStore(v.dir)
	if err != nil {
		return err
	}
	defer unlock()
	type blob struct {
		path string
		data []byte
	}
	blobs := make([]blob, 0, len(items))
	for _, it := range items {
		path, err := v.pathFor(it.Key)
		if err != nil {
			return err
		}
		ciphertext, err := v.backend.Encrypt(it.Value)
		if err != nil {
			return err
		}
		blobs = append(blobs, blob{path, ciphertext})
	}
	if err := WriteScaffold(v.dir); err != nil {
		return err
	}
	for _, b := range blobs {
		if err := os.MkdirAll(filepath.Dir(b.path), dirPerm); err != nil {
			return fmt.Errorf("creating scope directory: %w", err)
		}
		if err := atomicWrite(b.path, b.data, filePerm); err != nil {
			return err
		}
	}
	return v.commit(message)
}

// writeKey encrypts and writes one key's file without committing.
func (v *Vault) writeKey(key string, value []byte) error {
	path, err := v.pathFor(key)
	if err != nil {
		return err
	}
	if err := WriteScaffold(v.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("creating scope directory: %w", err)
	}
	ciphertext, err := v.backend.Encrypt(value)
	if err != nil {
		return err
	}
	return atomicWrite(path, ciphertext, filePerm)
}

// Delete removes key (and prunes now-empty scope directories), then commits.
func (v *Vault) Delete(key string) error {
	unlock, err := lockStore(v.dir)
	if err != nil {
		return err
	}
	defer unlock()
	if err := v.removeKey(key); err != nil {
		return err
	}
	return v.commit("rm " + key)
}

// DeleteMany removes several keys and commits once with the given message.
func (v *Vault) DeleteMany(keys []string, message string) error {
	unlock, err := lockStore(v.dir)
	if err != nil {
		return err
	}
	defer unlock()
	for _, key := range keys {
		if err := v.removeKey(key); err != nil {
			return err
		}
	}
	return v.commit(message)
}

// removeKey deletes one key's file and prunes empty scope dirs, without committing.
func (v *Vault) removeKey(key string) error {
	path, err := v.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("key %q %w", key, ErrNotFound)
		}
		return err
	}
	v.pruneEmptyDirs(filepath.Dir(path))
	return nil
}

// Rename moves a key. No re-encryption: the wrapping key derives from the
// in-file salt, not the path. Errors if dst exists or src is missing.
func (v *Vault) Rename(from, to string) error {
	unlock, err := lockStore(v.dir)
	if err != nil {
		return err
	}
	defer unlock()
	src, err := v.pathFor(from)
	if err != nil {
		return err
	}
	dst, err := v.pathFor(to)
	if err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("source and destination are the same: %q", from)
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("key %q %w", from, ErrNotFound)
		}
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("key %q already exists", to)
	}
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("creating scope directory: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	v.pruneEmptyDirs(filepath.Dir(src))
	return v.commit(fmt.Sprintf("mv %s %s", from, to))
}

// List returns all keys, sorted. Scope directories are reflected as slash paths.
func (v *Vault) List() ([]string, error) {
	var keys []string
	err := filepath.WalkDir(v.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ageExt) {
			return nil // store.json, README.md, anything non-secret
		}
		rel, err := filepath.Rel(v.dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(strings.TrimSuffix(rel, ageExt))
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

// Scopes returns every unique scope prefix (directory path) in the store, sorted.
func (v *Vault) Scopes() ([]string, error) {
	keys, err := v.List()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var scopes []string
	for _, k := range keys {
		parts := strings.Split(k, "/")
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			if !seen[prefix] {
				seen[prefix] = true
				scopes = append(scopes, prefix)
			}
		}
	}
	sort.Strings(scopes)
	return scopes, nil
}

func (v *Vault) commit(message string) error {
	if v.committer == nil {
		return nil
	}
	return v.committer.Commit(message)
}

// pathFor validates key and maps it to its on-disk file path.
func (v *Vault) pathFor(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return filepath.Join(v.dir, filepath.FromSlash(key)) + ageExt, nil
}

// validateKey rejects anything that isn't a clean, scope-delimited key, in
// particular path-traversal attempts that could write outside the store.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("invalid key %q: must not start or end with %q", key, "/")
	}
	if strings.ContainsRune(key, '~') {
		return fmt.Errorf("invalid key %q: '~' is reserved for version references (KEY~N)", key)
	}
	// Keys are file paths and env-var-ish names, so each '/'-separated segment is
	// restricted to [A-Za-z0-9_-]: portable across machines and filesystems, no
	// Unicode-normalization collisions, no path traversal, no surprises.
	for _, seg := range strings.Split(key, "/") {
		if seg == "" {
			return fmt.Errorf("invalid key %q: empty scope segment", key)
		}
		for _, r := range seg {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return fmt.Errorf("invalid key %q: only letters, digits, '_', '-', and '/' separators are allowed", key)
			}
		}
	}
	return nil
}

// scaffoldFiles are the unencrypted, static files every store carries besides
// store.json: the break-glass README and the git allowlist/attributes.
var scaffoldFiles = []struct{ name, content string }{
	{"README.md", readmeContent},
	{".gitignore", gitignoreContent},
	{".gitattributes", gitattributesContent},
}

// WriteScaffold writes any missing static store file (README, .gitignore,
// .gitattributes), so creating a store and writing to one share a single code
// path, and a write self-heals a store whose scaffolding was deleted or arrived
// incomplete (e.g. a restored .gitignore re-arms the default-deny allowlist
// before the next commit). It never overwrites an existing file, so a
// customized README is preserved. store.json is not written here: it carries the
// key fingerprint and is created when the store is opened (see session.Create).
func WriteScaffold(dir string) error {
	for _, f := range scaffoldFiles {
		p := filepath.Join(dir, f.name)
		switch _, err := os.Stat(p); {
		case err == nil:
			continue // present, leave it be
		case !os.IsNotExist(err):
			return err
		}
		if err := os.WriteFile(p, []byte(f.content), filePerm); err != nil {
			return fmt.Errorf("writing %s: %w", f.name, err)
		}
	}
	return nil
}

// pruneEmptyDirs removes empty scope directories from dir up to (not including)
// the store root. os.Remove fails on a non-empty dir, which stops the walk.
func (v *Vault) pruneEmptyDirs(dir string) {
	for dir != v.dir && strings.HasPrefix(dir, v.dir) {
		if err := os.Remove(dir); err != nil {
			return // non-empty or gone
		}
		dir = filepath.Dir(dir)
	}
}

// atomicWrite writes data to a temp file then renames it into place.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	success = true
	return nil
}

// gitignoreContent is a default-deny allowlist: the store's git repo tracks only
// encrypted secrets, the descriptor, and the README, never stray plaintext,
// editor junk, or the atomic-write temp files. Users can `git add -f` to override.
const gitignoreContent = `# vars store: commit only encrypted secrets, the descriptor, and this README.
*
!*/
!*.age
!/store.json
!/README.md
!/.gitignore
!/.gitattributes
`

// gitattributesContent marks encrypted files as binary so git never text-merges
// them (which would inject conflict markers into ciphertext) and never applies
// line-ending conversion (which would corrupt the bytes under core.autocrlf). A
// conflict on a key then resolves cleanly as a whole-file "pick a side".
const gitattributesContent = "*.age binary\n"

// readmeContent is written as README.md at the store root, so the directory
// explains itself and documents how to recover secrets without the vars binary.
const readmeContent = "# vars store\n\n" +
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
