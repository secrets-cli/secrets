package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(getCmd)
}

var getCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a key from the store",
	Long: `Print one value to stdout with no trailing newline. Pipes cleanly.

KEY~N is this key's state N commits back in its own history (not a global commit):
KEY~0 = latest committed state, KEY~1 the one before, and so on. The ~N matches the
tags shown by ` + "`vars log <key>`" + `. If that state was a removal, it has no value:
nothing is printed and the command exits non-zero (a removed key's last value is
then KEY~1). The ~ borrows git's syntax, but counts only commits to this key.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		base, n, isRef, perr := parseVersionRef(args[0])
		if perr != nil {
			return UserError(perr.Error())
		}

		var val []byte
		if isRef {
			val, err = v.GetVersion(base, n)
		} else {
			val, err = v.Get(args[0])
		}
		if err != nil {
			// Surface the real error: "not found" for a missing key, or a
			// decryption/key-mismatch message — don't conflate them.
			return UserError(err.Error())
		}

		fmt.Fprint(os.Stdout, string(val))
		return nil
	},
}

// parseVersionRef splits a "KEY~N" version reference. It returns isRef=false for
// a plain key (no '~'), and an error for a malformed reference.
func parseVersionRef(s string) (base string, n int, isRef bool, err error) {
	i := strings.LastIndexByte(s, '~')
	if i < 0 {
		return s, 0, false, nil
	}
	n, convErr := strconv.Atoi(s[i+1:])
	if s[:i] == "" || convErr != nil || n < 0 {
		return "", 0, true, fmt.Errorf("invalid version reference %q (use KEY~N, e.g. RPC_URL~2)", s)
	}
	return s[:i], n, true, nil
}
