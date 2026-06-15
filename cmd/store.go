package cmd

import (
	"fmt"
	"os"

	"github.com/vars-cli/vars/internal/crypto/sshderive"
	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/session"
	"github.com/vars-cli/vars/internal/vault"
)

// storeDir returns the store directory (VARS_STORE_DIR / XDG / default).
func storeDir() string { return vault.DefaultDir() }

// openVault opens the store, creating it on first use. All store-touching
// commands go through here.
func openVault() (*vault.Vault, error) {
	dir := storeDir()
	if !vault.Exists(dir) {
		if err := firstRun(dir); err != nil {
			return nil, err
		}
	}
	v, err := session.Open(dir)
	if err != nil {
		return nil, UserError(err.Error())
	}
	return v, nil
}

// firstRun creates the store, selecting which SSH key to bind it to.
func firstRun(dir string) error {
	fmt.Fprintf(os.Stderr, "No store found, creating one at:\n  %s\n\n", dir)

	signers, err := session.UsableInitSigners()
	if err != nil {
		return UserError(err.Error())
	}

	signer := signers[0]
	if len(signers) > 1 {
		signer, err = chooseKey(signers)
		if err != nil {
			return UserError(err.Error())
		}
	}
	fmt.Fprintf(os.Stderr, "Using SSH key %s\n", signer.Fingerprint())

	if err := session.Create(dir, signer); err != nil {
		return InternalError(err.Error())
	}
	fmt.Fprintf(os.Stderr, "Store created (encrypted to your SSH key, versioned with git).\n")
	return nil
}

// chooseKey prompts the user to pick among multiple usable SSH keys.
func chooseKey(signers []*sshderive.Signer) (*sshderive.Signer, error) {
	fmt.Fprintln(os.Stderr, "Multiple SSH keys available, choose one to encrypt this store:")
	for i, s := range signers {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, s.Fingerprint())
	}
	for {
		line, err := stdinPrompter().Line(fmt.Sprintf("Key [1-%d]: ", len(signers)))
		if err != nil {
			return nil, err
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(signers) {
			return signers[n-1], nil
		}
		fmt.Fprintln(os.Stderr, "Please enter a valid number.")
	}
}

// hintSync nudges the user to push after a mutation, but only when the store is
// a git repo with a remote configured (otherwise there's nowhere to sync).
func hintSync(dir string) {
	if git.Available() && git.IsRepo(dir) && git.New(dir).HasRemote() {
		fmt.Fprintln(os.Stderr, "Run 'vars sync' to push to your other machines.")
	}
}
