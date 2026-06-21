package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vars-cli/vars/internal/envfile"
	"github.com/vars-cli/vars/internal/vault"
)

var (
	importReplace bool
	importSkip    bool
)

func init() {
	importCmd.Flags().BoolVar(&importReplace, "replace", false, "Replace conflicting keys without confirmation")
	importCmd.Flags().BoolVar(&importSkip, "skip", false, "Skip conflicting keys without prompting")
	rootCmd.AddCommand(importCmd)
}

var importCmd = &cobra.Command{
	Use:   "import [scope] <file>",
	Short: "Import keys from a .env file",
	Long: `Import key-value pairs from a .env file into the store.

Without a scope, keys are imported as-is.
With a scope, keys are prefixed: vars import prod .env → prod/KEY.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if importReplace && importSkip {
			return UserError("--replace and --skip are mutually exclusive")
		}

		var scope, filePath string
		if len(args) == 2 {
			scope, filePath = args[0], args[1]
		} else {
			filePath = args[0]
		}

		f, err := os.Open(filePath)
		if err != nil {
			return UserError(fmt.Sprintf("opening file: %v", err))
		}
		defer f.Close()

		entries, err := envfile.Parse(f)
		if err != nil {
			return UserError(fmt.Sprintf("parsing file: %v", err))
		}
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "No entries found")
			return nil
		}
		if scope != "" {
			for i := range entries {
				entries[i].Key = scope + "/" + entries[i].Key
			}
		}

		v, err := openVault()
		if err != nil {
			return err
		}
		isTTY := term.IsTerminal(int(os.Stdin.Fd()))

		var pending []vault.Item
		var imported, replaced, skipped int

	entryLoop:
		for _, e := range entries {
			key, value := e.Key, e.Value
			for {
				existing, getErr := v.Get(key)
				if getErr != nil {
					if !errors.Is(getErr, vault.ErrNotFound) {
						// Invalid key (e.g. a bad scope) or a decrypt failure — abort
						// before writing anything, don't misclassify it as a new key.
						return UserError(getErr.Error())
					}
					pending = append(pending, vault.Item{Key: key, Value: []byte(value)})
					imported++
					continue entryLoop
				}
				if string(existing) == value {
					skipped++
					continue entryLoop
				}
				if importSkip {
					fmt.Fprintf(os.Stderr, "Skipped %s\n", key)
					skipped++
					continue entryLoop
				}
				if importReplace {
					pending = append(pending, vault.Item{Key: key, Value: []byte(value)})
					replaced++
					continue entryLoop
				}
				if !isTTY {
					return UserError("conflicting keys found; use --replace or --skip to resolve non-interactively")
				}

				fmt.Fprintf(os.Stderr, "\n%s already exists.\n  current:  %s\n  imported: %s\n", key, preview(string(existing)), preview(value))
				action, newKey, err := resolveConflict(stdinPrompter())
				if err != nil {
					return UserError(err.Error())
				}
				switch action {
				case actionReplace:
					pending = append(pending, vault.Item{Key: key, Value: []byte(value)})
					replaced++
					continue entryLoop
				case actionRename:
					key = newKey
					// the new key may also exist: re-check it for conflicts
				default: // actionSkip
					fmt.Fprintf(os.Stderr, "Skipped %s\n", key)
					skipped++
					continue entryLoop
				}
			}
		}

		if len(pending) > 0 {
			msg := fmt.Sprintf("import %d keys from %s", len(pending), filePath)
			if err := v.SetMany(pending, msg); err != nil {
				return UserError(err.Error())
			}
		}

		fmt.Fprintf(os.Stderr, "Imported %d, replaced %d, skipped %d\n", imported, replaced, skipped)
		hintSync(storeDir())
		return nil
	},
}
