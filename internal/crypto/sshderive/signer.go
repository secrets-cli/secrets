// Package sshderive implements the ssh-v1 crypto backend: each file's age
// file-key is wrapped by a symmetric key derived from a deterministic SSH
// signature. Signing is delegated to ssh-agent (preferred) or a private key
// file, so vars needs no daemon of its own — ssh-agent already caches the key,
// and signing is the one operation it exposes.
//
// The signature uses OpenSSH's SSHSIG format (the one `ssh-keygen -Y sign`
// produces) with namespace "vars.store.v1". That gives two things for free:
// domain separation from any other tool signing with the same key, and a
// break-glass recovery path that uses only standard `ssh-keygen`.
//
// Only deterministic key types work: Ed25519 and RSA (rsa-sha2-512). ECDSA,
// DSA, and FIDO/security keys sign with a random nonce, so the same salt would
// derive a different key each time; they are rejected.
package sshderive

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	// namespace domain-separates vars signatures (SSHSIG namespace field).
	namespace = "vars.store.v1"
	// sshsigMagic and sshsigHashAlg match OpenSSH's SSHSIG defaults so that
	// `ssh-keygen -Y sign -n vars.store.v1` reproduces our signature byte-for-byte.
	sshsigMagic   = "SSHSIG"
	sshsigHashAlg = "sha512"
)

// Signer signs the SSHSIG envelope of a salt and derives a 32-byte wrapping key
// from the result. The same key + salt always yields the same wrapping key
// (deterministic), which is what lets any machine holding the key decrypt.
type Signer struct {
	signRaw     func(data []byte) (*ssh.Signature, error) // signs the SSHSIG to-be-signed blob
	pub         ssh.PublicKey
	fingerprint string
}

// Fingerprint returns the SHA256 fingerprint (e.g. "SHA256:…") that identifies
// the key. It is stored in store.json to pin which key a store requires.
func (s *Signer) Fingerprint() string { return s.fingerprint }

// deriveKey returns the 32-byte wrapping key for a given per-file salt.
func (s *Signer) deriveKey(salt []byte) ([]byte, error) {
	sig, err := s.signRaw(sshsigSignedData(namespace, salt))
	if err != nil {
		return nil, fmt.Errorf("signing for key derivation: %w", err)
	}
	// HKDF over the SSH wire signature (the exact bytes recoverable from a
	// `ssh-keygen -Y sign` .sig file), salted with the per-file salt.
	r := hkdf.New(sha256.New, wireSignature(sig), salt, []byte(namespace+"/fileKey"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// sshsigSignedData builds the exact byte sequence OpenSSH signs for an SSHSIG
// signature: MAGIC || string(namespace) || string(reserved) || string(hashAlg)
// || string(H(message)). See OpenSSH PROTOCOL.sshsig.
func sshsigSignedData(ns string, message []byte) []byte {
	h := sha512.Sum512(message)
	var b []byte
	b = append(b, sshsigMagic...) // 6 raw bytes, not length-prefixed
	b = append(b, sshString([]byte(ns))...)
	b = append(b, sshString(nil)...) // reserved: empty
	b = append(b, sshString([]byte(sshsigHashAlg))...)
	b = append(b, sshString(h[:])...)
	return b
}

// wireSignature encodes an ssh.Signature as string(format) || string(blob),
// matching the inner signature field of an SSHSIG blob.
func wireSignature(sig *ssh.Signature) []byte {
	out := sshString([]byte(sig.Format))
	return append(out, sshString(sig.Blob)...)
}

// sshString encodes b as an SSH string: uint32 big-endian length || bytes.
func sshString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

// FingerprintOfPubFile returns the SHA256 fingerprint of an OpenSSH public-key
// file (e.g. ~/.ssh/id_vars.pub). It needs no passphrase, so it's usable to find
// which key file matches a store, even when that key is passphrase-protected.
func FingerprintOfPubFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pub), nil
}

// supportedKeyType returns nil for deterministic key types and a clear,
// actionable error otherwise.
func supportedKeyType(pub ssh.PublicKey) error {
	switch pub.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoRSA:
		return nil
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return fmt.Errorf("ECDSA keys sign non-deterministically and can't be used by vars; use an Ed25519 key (ssh-keygen -t ed25519)")
	default:
		// Includes DSA (ssh-dss) and FIDO/sk- keys: non-deterministic or unsupported.
		return fmt.Errorf("SSH key type %q is not supported; vars needs a deterministic Ed25519 or RSA key (FIDO/sk- keys won't work)", pub.Type())
	}
}

// fromSSHSigner builds a Signer that signs in-process with the given ssh.Signer
// (the private-key-file path). RSA is pinned to rsa-sha2-512 so its signatures
// match the agent path and ssh-keygen.
func fromSSHSigner(ss ssh.Signer) (*Signer, error) {
	pub := ss.PublicKey()
	if err := supportedKeyType(pub); err != nil {
		return nil, err
	}
	isRSA := pub.Type() == ssh.KeyAlgoRSA
	signRaw := func(data []byte) (*ssh.Signature, error) {
		if isRSA {
			as, ok := ss.(ssh.AlgorithmSigner)
			if !ok {
				return nil, errors.New("RSA key does not support algorithm selection")
			}
			return as.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA512)
		}
		return ss.Sign(rand.Reader, data) // rand is ignored for Ed25519 (deterministic)
	}
	return &Signer{signRaw: signRaw, pub: pub, fingerprint: ssh.FingerprintSHA256(pub)}, nil
}

// fromAgentKey builds a Signer backed by an ssh-agent for a specific key.
func fromAgentKey(ag agent.Agent, pub ssh.PublicKey) *Signer {
	isRSA := pub.Type() == ssh.KeyAlgoRSA
	signRaw := func(data []byte) (*ssh.Signature, error) {
		if isRSA {
			ext, ok := ag.(agent.ExtendedAgent)
			if !ok {
				return nil, errors.New("ssh-agent does not support RSA SHA-2 signing")
			}
			return ext.SignWithFlags(pub, data, agent.SignatureFlagRsaSha512)
		}
		return ag.Sign(pub, data)
	}
	return &Signer{signRaw: signRaw, pub: pub, fingerprint: ssh.FingerprintSHA256(pub)}
}

// FromFile loads an unencrypted private key file. Passphrase-protected keys are
// not read directly: load them into ssh-agent (vars signs through the agent),
// which is the supported path. A passphrase-protected file yields a clear error
// pointing there.
func FromFile(path string) (*Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading SSH key %s: %w", path, err)
	}
	ss, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var pm *ssh.PassphraseMissingError
		if errors.As(err, &pm) {
			return nil, fmt.Errorf("SSH key %s is passphrase-protected; load it with `ssh-add` (vars signs through ssh-agent)", path)
		}
		return nil, err
	}
	return fromSSHSigner(ss)
}

// FromAgent returns a Signer for the agent key matching fingerprint. If
// fingerprint is empty, it selects the single usable key, erroring when zero or
// several are present (callers should pin a fingerprint via store.json).
func FromAgent(ag agent.Agent, fingerprint string) (*Signer, error) {
	keys, err := ag.List()
	if err != nil {
		return nil, fmt.Errorf("listing ssh-agent keys: %w", err)
	}

	var usable []*Signer
	for _, k := range keys {
		pub, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			continue
		}
		if fingerprint != "" {
			if ssh.FingerprintSHA256(pub) != fingerprint {
				continue
			}
			// The requested key must be usable — surface why if not.
			if err := supportedKeyType(pub); err != nil {
				return nil, err
			}
			return fromAgentKey(ag, pub), nil
		}
		if supportedKeyType(pub) == nil {
			usable = append(usable, fromAgentKey(ag, pub))
		}
	}

	if fingerprint != "" {
		return nil, fmt.Errorf("ssh-agent does not hold the key this store requires (%s); load it with ssh-add", fingerprint)
	}
	switch len(usable) {
	case 0:
		return nil, errors.New("no usable Ed25519 or RSA key found in ssh-agent")
	case 1:
		return usable[0], nil
	default:
		return nil, errors.New("multiple SSH keys in ssh-agent; specify which one (set it at init or via VARS_SSH_KEY)")
	}
}

// AgentSigners returns a Signer for every usable (Ed25519/RSA) key held by the
// agent, in agent order. Used to offer key choices when creating a new store.
func AgentSigners(ag agent.Agent) ([]*Signer, error) {
	keys, err := ag.List()
	if err != nil {
		return nil, fmt.Errorf("listing ssh-agent keys: %w", err)
	}
	var signers []*Signer
	for _, k := range keys {
		pub, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			continue
		}
		if supportedKeyType(pub) == nil {
			signers = append(signers, fromAgentKey(ag, pub))
		}
	}
	return signers, nil
}

// DialAgent connects to the ssh-agent at $SSH_AUTH_SOCK. The caller closes conn.
func DialAgent() (agent.ExtendedAgent, net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is not set (no ssh-agent running)")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to ssh-agent: %w", err)
	}
	return agent.NewClient(conn), conn, nil
}
