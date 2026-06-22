package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	scopeCmd.AddCommand(scopeLsCmd)
	rootCmd.AddCommand(scopeCmd)
}

var scopeCmd = &cobra.Command{
	Use:   "scope",
	Short: "Manage key scopes",
}

var scopeLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all scope prefixes in the store",
	Long: `List all unique scope prefixes found across all store keys, at all levels.

"main/dev/RPC_URL" contributes both "main" and "main/dev".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		scopes, err := v.Scopes()
		if err != nil {
			return InternalError(err.Error())
		}
		for _, s := range scopes {
			fmt.Fprintln(os.Stdout, s)
		}
		return nil
	},
}
