package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/format"
)

var (
	dumpFish   bool
	dumpDotenv bool
)

func init() {
	dumpCmd.Flags().BoolVar(&dumpDotenv, "dotenv", false, "Output as KEY=value (for docker --env-file etc.)")
	dumpCmd.Flags().BoolVar(&dumpFish, "fish", false, "Output in fish shell format (set -x KEY value)")
	rootCmd.AddCommand(dumpCmd)
}

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Dump all variables from the store",
	Long: `Print all key/value pairs from the store. No manifest involved.
Intended for debugging and migration only.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		formatter := format.Posix
		if dumpFish {
			formatter = format.Fish
		} else if dumpDotenv {
			formatter = format.Dotenv
		}

		fmt.Fprintln(os.Stderr, "vars: dumping all variables from the store")

		v, err := openVault()
		if err != nil {
			return err
		}
		keys, err := v.List()
		if err != nil {
			return InternalError(err.Error())
		}
		for _, key := range keys {
			val, err := v.Get(key)
			if err != nil {
				return InternalError(fmt.Sprintf("decrypting %q: %v", key, err))
			}
			fmt.Fprintln(os.Stdout, formatter(key, string(val)))
		}
		return nil
	},
}
