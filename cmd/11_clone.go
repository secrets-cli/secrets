package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/session"
	"github.com/vars-cli/vars/internal/vault"
)

func init() {
	rootCmd.AddCommand(cloneCmd)
}

var cloneCmd = &cobra.Command{
	Use:   "clone <remote>",
	Short: "Clone an existing store from a git remote",
	Long: `Clone an existing store into your local store directory by cloning its git
remote (e.g. to set vars up from an encrypted remote store). Use this instead of
creating a fresh store, which would diverge from the remote. git clone sets
'origin', so 'vars sync' works right away.

If a store already exists locally, clone replaces it only when it holds no secrets.
The SSH key that authenticates the clone may differ from the key the store is encrypted
to; this reports whether the latter is available to read secrets.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remote, dir := args[0], storeDir()
		if vault.Exists(dir) {
			// Replace an empty store (no secrets to lose); never clobber one with secrets.
			v, err := session.Open(dir)
			if err != nil {
				return UserError(err.Error())
			}
			keys, err := v.List()
			if err != nil {
				return InternalError(err.Error())
			}
			if len(keys) > 0 {
				return UserError(fmt.Sprintf("a store with %d secret(s) already exists at %s; clone won't overwrite it (move it aside, or set VARS_STORE_DIR elsewhere)", len(keys), dir))
			}
			fmt.Fprintf(os.Stderr, "Replacing the empty store at %s\n", dir)
			if err := os.RemoveAll(dir); err != nil {
				return InternalError(fmt.Sprintf("removing the empty store: %v", err))
			}
		}
		if !git.Available() {
			return UserError("git is not installed; it's needed to clone a store")
		}
		if err := git.Clone(remote, dir); err != nil {
			return &ExitError{Code: 1} // git already wrote its own error
		}
		// The 0700 store root is the access boundary (git can't record file modes,
		// and a later pull would reset them anyway, so we don't chase per-file modes).
		if err := os.Chmod(dir, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "vars: warning: could not set %s to 0700: %v\n", dir, err)
		}
		if !vault.Exists(dir) {
			return UserError(fmt.Sprintf("cloned into %s, but there's no store.json — is this a vars store?", dir))
		}
		fmt.Fprintf(os.Stderr, "Cloned into %s\n", dir)

		meta, err := vault.ReadMeta(dir)
		if err != nil {
			return UserError(err.Error())
		}
		switch {
		case meta.Scheme != session.Scheme:
			fmt.Fprintf(os.Stderr, "Note: store scheme %q is not supported by this vars (%q); upgrade to read it.\n", meta.Scheme, session.Scheme)
		case !session.KeyAvailable(meta.KeyFingerprint):
			fmt.Fprintf(os.Stderr, "This store is encrypted to SSH key %s.\n", meta.KeyFingerprint)
			fmt.Fprintln(os.Stderr, "Load that key (`ssh-add`, or point VARS_SSH_KEY at it) to read secrets.")
		}
		return nil
	},
}
