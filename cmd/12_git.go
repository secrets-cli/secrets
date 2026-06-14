package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/git"
	"github.com/vars-cli/vars/internal/vault"
)

func init() {
	rootCmd.AddCommand(gitCmd)
	rootCmd.AddCommand(syncCmd)
}

var gitCmd = &cobra.Command{
	Use:                "git [args...]",
	Short:              "Run git in the store directory",
	Long:               "Pass arguments straight through to git, run inside the store directory.\n\n  vars git remote add origin <url>\n  vars git log\n  vars git status",
	DisableFlagParsing: true, // forward flags (e.g. -u) to git untouched
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := storeDir()
		if !vault.Exists(dir) {
			return UserError("no store yet — run `vars` to create one")
		}
		if !git.Available() {
			return UserError("git is not installed")
		}
		if err := git.New(dir).Passthrough(args); err != nil {
			// git already wrote to stderr; surface a non-zero exit.
			return &ExitError{Code: 1, Message: "git: " + err.Error()}
		}
		return nil
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Pull and push the store to its git remote",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := storeDir()
		if !vault.Exists(dir) {
			return UserError("no store yet — run `vars` to create one")
		}
		if !git.Available() || !git.IsRepo(dir) {
			return UserError("the store is not a git repo; nothing to sync")
		}
		if err := git.New(dir).Sync(); err != nil {
			return UserError(err.Error())
		}
		fmt.Fprintln(os.Stderr, "Synced.")
		return nil
	},
}
