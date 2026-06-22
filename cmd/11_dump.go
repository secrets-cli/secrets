package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vars-cli/vars/internal/format"
	"github.com/vars-cli/vars/internal/session"
	"github.com/vars-cli/vars/internal/vault"
)

var (
	dumpFish   bool
	dumpDotenv bool
	dumpForce  bool
)

func init() {
	dumpCmd.Flags().BoolVar(&dumpDotenv, "dotenv", false, "Output as KEY=value (for docker --env-file etc.)")
	dumpCmd.Flags().BoolVar(&dumpFish, "fish", false, "Output in fish shell format (set -x KEY value)")
	dumpCmd.Flags().BoolVarP(&dumpForce, "force", "f", false, "Skip the confirmation prompt (for non-interactive use)")
	rootCmd.AddCommand(dumpCmd)
}

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Dump all variables from the store",
	Long: `Print every key and value from the store, in plaintext. No manifest involved.
Use it for migrating or debugging. For loading secrets into a process, use 'vars resolve' instead.

Prompts for confirmation unless --force is given.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		formatter := format.Posix
		if dumpFish {
			formatter = format.Fish
		} else if dumpDotenv {
			formatter = format.Dotenv
		}

		v, err := openVault()
		if err != nil {
			return err
		}
		keys, err := v.List() // no key needed; gives the count for the confirmation
		if err != nil {
			return InternalError(err.Error())
		}

		// dump prints every secret in plaintext — the deliberate exception to the
		// store's whole purpose. Confirm first (before unlocking, so a cancel is
		// free), unless --force; refuse non-interactively so a stray script can't
		// mass-export every secret without explicit intent.
		if !dumpForce && len(keys) > 0 {
			fmt.Fprintf(os.Stderr, "This prints all %d secret(s) in plaintext.\n", len(keys))
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return UserError("dumping all secrets in plaintext requires confirmation; use --force for non-interactive use")
			}
			answer, err := stdinPrompter().Line("Continue? [y/N] ")
			if err != nil {
				return UserError(err.Error())
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Fprintln(os.Stderr, "Aborted")
				return nil
			}
		}

		// Unlock the key (auto ssh-add when there's a terminal). Fail once here if
		// it can't be resolved, rather than warning per key; the per-file skip
		// below is only for individual unreadable files.
		if meta, merr := vault.ReadMeta(storeDir()); merr == nil {
			if err := session.EnsureKey(meta.KeyFingerprint); err != nil {
				return UserError(err.Error())
			}
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
