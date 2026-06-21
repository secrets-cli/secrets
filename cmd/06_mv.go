package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var mvForce bool

func init() {
	mvCmd.Flags().BoolVarP(&mvForce, "force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(mvCmd)
}

var mvCmd = &cobra.Command{
	Use:   "mv <from> <to>",
	Short: "Rename a key in the store",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		if !mvForce {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return UserError("rename requires confirmation; use --force for non-interactive use")
			}
			fmt.Fprintf(os.Stderr, "Rename %s → %s\n", args[0], args[1])
			answer, err := stdinPrompter().Line("Confirm? [y/N] ")
			if err != nil {
				return UserError(err.Error())
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Fprintln(os.Stderr, "Aborted")
				return nil
			}
		}

		if err := v.Rename(args[0], args[1]); err != nil {
			return UserError(err.Error())
		}

		fmt.Fprintf(os.Stderr, "Renamed: %s → %s\n", args[0], args[1])
		hintSync(storeDir())
		return nil
	},
}
