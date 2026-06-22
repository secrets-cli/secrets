package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/vault"
)

func init() {
	rootCmd.AddCommand(logCmd)
}

var logCmd = &cobra.Command{
	Use:   "log <key>",
	Short: "Show a key's change history (newest first)",
	Long: `List a key's committed states, newest first, with local time. Each line is
tagged with the ~N you pass to ` + "`vars get <key>~N`" + ` (~0 = latest state, ~1 = before).
A commit that removed the key shows as "(removed)", a state with no value; every
other line is a stored value. Requires the store to be a git repo.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Reads git metadata only (no decryption), so it needs neither the SSH key
		// nor a working-tree file: a removed key still has history.
		dir := storeDir()
		if !vault.Exists(dir) {
			return UserError("no store yet: run `vars` to create one")
		}
		if !git.Available() || !git.IsRepo(dir) {
			return UserError("history is unavailable: the store is not a git repo")
		}
		lines, err := git.New(dir).Log(args[0] + ".age")
		if err != nil {
			return InternalError(err.Error())
		}
		if len(lines) == 0 {
			fmt.Fprintf(os.Stderr, "No history for %q\n", args[0])
			return nil
		}
		// Tag each line with the ~N that retrieves it (`vars get <key>~N`): the
		// list is newest-first, so its index is exactly that N.
		for i, l := range lines {
			fmt.Fprintf(os.Stdout, "~%d  %s\n", i, l)
		}
		return nil
	},
}
