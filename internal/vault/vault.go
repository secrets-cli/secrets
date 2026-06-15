// Package vault is the v3 store: one age-encrypted file per secret, scopes as
// directories, rooted at a single directory that is usually a git repo.
//
// It does encrypted file CRUD only. Versioning is delegated to an optional
// Committer (implemented by the git package), so the vault knows nothing about
// git and stays trivially testable.
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
	if err := v.writeKey(key, value); err != nil {
		return err
	}
	return v.commit("set " + key)
}

// SetMany writes several keys and commits once with the given message.
func (v *Vault) SetMany(items []Item, message string) error {
	for _, it := range items {
		if err := v.writeKey(it.Key, it.Value); err != nil {
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
	if err := v.removeKey(key); err != nil {
		return err
	}
	return v.commit("rm " + key)
}

// DeleteMany removes several keys and commits once with the given message.
func (v *Vault) DeleteMany(keys []string, message string) error {
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
	src, err := v.pathFor(from)
	if err != nil {
		return err
	}
	dst, err := v.pathFor(to)
	if err != nil {
		return err
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
	if strings.ContainsAny(key, "\x00\\") {
		return fmt.Errorf("invalid key %q: contains an illegal character", key)
	}
	if strings.ContainsRune(key, '~') {
		return fmt.Errorf("invalid key %q: '~' is reserved for version references (KEY~N)", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid key %q: empty or relative path segment", key)
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
