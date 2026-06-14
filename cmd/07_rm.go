package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(rmCmd)
}

var rmForce bool

var rmCmd = &cobra.Command{
	Use:   "rm <key> [key...]",
	Short: "Remove one or more keys from the store",
	Long:  `Delete keys from the store. Prompts for confirmation unless --force is used.`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		// Verify all keys exist before prompting or deleting.
		for _, key := range args {
			if !v.Has(key) {
				return UserError(fmt.Sprintf("key %q not found in store", key))
			}
		}

		if !rmForce {
			if len(args) == 1 {
				fmt.Fprintf(os.Stderr, "Removing %s.\n", args[0])
			} else {
				fmt.Fprintf(os.Stderr, "Removing %d keys:\n", len(args))
				for _, key := range args {
					fmt.Fprintf(os.Stderr, "  %s\n", key)
				}
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return UserError("deletion requires confirmation; use --force for non-interactive use")
			}
			answer, err := stdinPrompter().Line("Confirm? [y/N] ")
			if err != nil {
				return UserError(err.Error())
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
		}

		var derr error
		if len(args) == 1 {
			derr = v.Delete(args[0])
		} else {
			derr = v.DeleteMany(args, "rm "+strings.Join(args, " "))
		}
		if derr != nil {
			return UserError(derr.Error())
		}

		if len(args) == 1 {
			fmt.Fprintln(os.Stderr, "Removed.")
		} else {
			fmt.Fprintf(os.Stderr, "Removed %d keys.\n", len(args))
		}
		hintSync(storeDir())
		return nil
	},
}
