// Package crypto defines the interface for encryption backends.
//
// The Backend interface abstracts encryption and decryption so the store and
// CLI layers stay independent of how keys are managed. The only implementation
// today is the ssh-v1 backend (internal/crypto/sshderive); the seam keeps a
// future backend (e.g. a hardware token) swappable without touching callers.
package crypto

// Backend encrypts and decrypts arbitrary byte slices. Implementations own key
// management; callers only see opaque bytes in and out.
type Backend interface {
	Encrypt(plaintext []byte) (ciphertext []byte, err error)
	Decrypt(ciphertext []byte) (plaintext []byte, err error)
}
