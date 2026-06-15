package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	setReplace bool
	setSkip    bool
)

func init() {
	setCmd.Flags().BoolVar(&setReplace, "replace", false, "Replace existing key without confirmation")
	setCmd.Flags().BoolVar(&setSkip, "skip", false, "Skip if key already exists")
	rootCmd.AddCommand(setCmd)
}

var setCmd = &cobra.Command{
	Use:   "set <key> [value]",
	Short: "Add or update a key in the store",
	Long: `Write a key-value pair to the store. If value is omitted, prompts
interactively with echo disabled (preferred, since inline values appear in
shell history).

  vars set KEY value      add/update inline (note: appears in shell history)
  vars set KEY            prompt for the value, masked
  vars set KEY -          read the value from stdin, verbatim (use for multi-line, e.g. a PEM)
  vars set KEY -- -value  for a value that starts with '-'`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if setReplace && setSkip {
			return UserError("--replace and --skip are mutually exclusive")
		}

		key := args[0]

		var value string
		switch {
		case len(args) == 2 && args[1] == "-":
			// Read the whole of stdin verbatim — the way to store multi-line or
			// piped values without the first-line truncation a prompt would impose.
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return UserError("reading value from stdin: " + err.Error())
			}
			value = string(data)
		case len(args) == 2:
			value = args[1]
		case term.IsTerminal(int(os.Stdin.Fd())):
			v, err := stdinPrompter().Secret("Value: ")
			if err != nil {
				return UserError(err.Error())
			}
			value = v
		default:
			// Non-interactive with no value: don't silently read a single line.
			return UserError("no value given; pass it as an argument, or pipe it with `vars set " + key + " -`")
		}

		v, err := openVault()
		if err != nil {
			return err
		}
		isTTY := term.IsTerminal(int(os.Stdin.Fd()))

		// Conflict resolution loop (handles rename re-checks).
		for {
			existing, getErr := v.Get(key)
			if getErr != nil {
				break // new key — no conflict
			}
			if string(existing) == value {
				fmt.Fprintln(os.Stderr, "Already set, nothing to do.")
				return nil
			}
			if setSkip {
				fmt.Fprintln(os.Stderr, "Skipped.")
				return nil
			}
			if setReplace {
				break
			}
			if !isTTY {
				return UserError("key already exists; use --replace or --skip")
			}

			fmt.Fprintf(os.Stderr, "\n%s already exists. New value will replace it.\n", key)
			choice, err := stdinPrompter().Line("[r]eplace  [n]ew name  [s]kip > ")
			if err != nil {
				return UserError(err.Error())
			}
			switch c := strings.ToLower(strings.TrimSpace(choice)); {
			case strings.HasPrefix(c, "r"):
				// proceed to set below
			case strings.HasPrefix(c, "n"):
				sfx, err := stdinPrompter().Line(fmt.Sprintf("Suffix (saved as %s_<suffix>): ", key))
				if err != nil {
					return UserError(err.Error())
				}
				sfx = strings.TrimSpace(strings.TrimPrefix(sfx, "_"))
				if sfx == "" {
					fmt.Fprintln(os.Stderr, "Suffix cannot be empty, skipping.")
					return nil
				}
				key = key + "_" + sfx
				continue // renamed key may be new — re-check
			default: // "s" or unrecognised
				fmt.Fprintln(os.Stderr, "Skipped.")
				return nil
			}
			break
		}

		if err := v.Set(key, []byte(value)); err != nil {
			return UserError(err.Error())
		}

		printManifestHint(key)
		fmt.Fprintln(os.Stderr, "Saved.")
		hintSync(storeDir())
		return nil
	},
}
