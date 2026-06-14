package sshderive

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/vars-cli/vars/internal/crypto"
)

// stanzaType identifies the vars recipient block in the age header.
const stanzaType = "vars-ssh-v1"

// saltLen is the per-file random salt size. It is not secret; it is stored in
// the stanza and signed to derive that file's wrapping key.
const saltLen = 32

// Ensure Backend implements crypto.Backend at compile time.
var _ crypto.Backend = (*Backend)(nil)

// Backend encrypts/decrypts using age, with the age file-key wrapped by an
// SSH-derived key (see Signer). Each file is a valid age file with one
// vars-ssh-v1 stanza; the standard age CLI cannot open it (the stanza is custom),
// which is expected — only vars (or a documented break-glass step) can.
type Backend struct{ signer *Signer }

// NewBackend returns a Backend bound to the given Signer.
func NewBackend(s *Signer) *Backend { return &Backend{signer: s} }

// Encrypt encrypts plaintext to a single vars-ssh-v1 age file.
func (b *Backend) Encrypt(plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, &recipient{b.signer})
	if err != nil {
		return nil, fmt.Errorf("initializing encryption: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("writing encrypted data: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("finalizing encryption: %w", err)
	}
	return buf.Bytes(), nil
}

// Decrypt decrypts a vars-ssh-v1 age file.
func (b *Backend) Decrypt(ciphertext []byte) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), &identity{b.signer})
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, fmt.Errorf("not encrypted to your SSH key (%s)", b.signer.Fingerprint())
		}
		return nil, fmt.Errorf("decryption failed: %w", err)
	}
	return io.ReadAll(r)
}

// recipient wraps the age file-key under the SSH-derived key. age calls Wrap
// once per encryption with a fresh file-key.
type recipient struct{ signer *Signer }

func (r *recipient) Wrap(fileKey []byte) ([]*age.Stanza, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := r.signer.deriveKey(salt)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	body := aead.Seal(nonce, nonce, fileKey, nil) // nonce-prefixed sealed file-key
	return []*age.Stanza{{
		Type: stanzaType,
		Args: []string{base64.RawStdEncoding.EncodeToString(salt)},
		Body: body,
	}}, nil
}

// identity unwraps the age file-key from the vars-ssh-v1 stanza.
type identity struct{ signer *Signer }

func (i *identity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	for _, s := range stanzas {
		if s.Type != stanzaType {
			continue
		}
		if len(s.Args) != 1 {
			return nil, errors.New("vars-ssh-v1 stanza: expected exactly one argument (salt)")
		}
		salt, err := base64.RawStdEncoding.DecodeString(s.Args[0])
		if err != nil {
			return nil, fmt.Errorf("vars-ssh-v1 stanza: invalid salt: %w", err)
		}
		key, err := i.signer.deriveKey(salt)
		if err != nil {
			return nil, err
		}
		aead, err := chacha20poly1305.New(key)
		if err != nil {
			return nil, err
		}
		if len(s.Body) < aead.NonceSize() {
			return nil, errors.New("vars-ssh-v1 stanza: truncated body")
		}
		nonce, sealed := s.Body[:aead.NonceSize()], s.Body[aead.NonceSize():]
		fileKey, err := aead.Open(nil, nonce, sealed, nil)
		if err != nil {
			// This stanza isn't for our key. Keep going: a file may carry
			// several vars-ssh-v1 stanzas (one per recipient/key) once
			// multi-recipient support is added.
			continue
		}
		return fileKey, nil
	}
	return nil, age.ErrIncorrectIdentity
}
