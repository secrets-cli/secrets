package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/git"
)

func init() {
	rootCmd.AddCommand(historyCmd)
}

var historyCmd = &cobra.Command{
	Use:   "history <key>",
	Short: "Show a key's change history (newest first)",
	Long:  `List the git commits that touched a key. Requires the store to be a git repo.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		if !v.Has(args[0]) {
			return UserError(fmt.Sprintf("key %q not found in store", args[0]))
		}

		dir := storeDir()
		if !git.Available() || !git.IsRepo(dir) {
			return UserError("history is unavailable: the store is not a git repo")
		}
		lines, err := git.New(dir).Log(args[0] + ".age")
		if err != nil {
			return InternalError(err.Error())
		}
		if len(lines) == 0 {
			fmt.Fprintln(os.Stderr, "No history yet.")
			return nil
		}
		for _, l := range lines {
			fmt.Fprintln(os.Stdout, l)
		}
		return nil
	},
}
