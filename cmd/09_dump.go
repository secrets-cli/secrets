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
		// Recovery/migration path: dump everything we can, warn on what we can't,
		// and exit non-zero if any key failed — one bad file must not hide the rest.
		failed := false
		for _, key := range keys {
			val, err := v.Get(key)
			if err != nil {
				fmt.Fprintf(os.Stderr, "vars: warning: skipping %q: %v\n", key, err)
				failed = true
				continue
			}
			if dumpDotenv && format.HasNewline(string(val)) {
				fmt.Fprintf(os.Stderr, "vars: warning: skipping %q: value has a newline, not representable in --dotenv\n", key)
				failed = true
				continue
			}
			fmt.Fprintln(os.Stdout, formatter(key, string(val)))
		}
		if failed {
			return InternalError("some keys could not be dumped (see warnings above)")
		}
		return nil
	},
}
