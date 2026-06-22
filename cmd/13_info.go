package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/session"
	"github.com/vars-cli/vars/internal/vault"
)

func init() {
	rootCmd.AddCommand(infoCmd)
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show store location, key, readiness, and git state",
	Long: `Print a summary of the store: where it lives, which SSH key it's
encrypted to and whether that key is available right now (and from where), how
many secrets and scopes it holds, and its local git state.

Needs no key and touches no network, so it's the command to run when you can't
decrypt and want to know why.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := storeDir()
		if !vault.Exists(dir) {
			return UserError(fmt.Sprintf("no vars store at %s (run `vars` to create one)", dir))
		}
		meta, err := vault.ReadMeta(dir)
		if err != nil {
			return UserError(err.Error())
		}

		scheme := meta.Scheme
		if scheme != session.Scheme {
			scheme += " (unsupported by this build)"
		}
		fmt.Printf("Store:    %s\n", dir)
		fmt.Printf("Scheme:   %s\n", scheme)
		fmt.Printf("Key:      %s  %s\n", meta.KeyFingerprint, session.KeyStatus(meta.KeyFingerprint))

		// Counts: lazy backend, so no key is needed to list the files.
		if v, oerr := session.Open(dir); oerr == nil {
			keys, _ := v.List()
			scopes, _ := v.Scopes()
			scopeWord := "scopes"
			if len(scopes) == 1 {
				scopeWord = "scope"
			}
			fmt.Printf("Secrets:  %d  (%d %s)\n", len(keys), len(scopes), scopeWord)
		}

		// Git: local state only (no fetch, so it stays offline/instant).
		switch {
		case !git.Available() || !git.IsRepo(dir):
			fmt.Println("Git:      not versioned")
		default:
			repo := git.New(dir)
			line := "repo, no remote"
			if url := repo.RemoteURL(); url != "" {
				line = "repo, remote " + url
			}
			if repo.HasUncommittedChanges() {
				line += " (uncommitted changes; run `vars sync`)"
			}
			fmt.Printf("Git:      %s\n", line)
		}
		return nil
	},
}
