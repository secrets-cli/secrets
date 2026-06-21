// Package cmd implements the CLI commands for vars.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/vault"
)

func init() {
	cobra.EnableCommandSorting = false
}

// Version is set at build time via ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "vars",
	Short: "An encrypted store for your environment variables",
	Long: `vars keeps your project secrets in one encrypted store, unlocked by the
SSH key you already have. Each value is a separate age-encrypted file in an optional
git repo, so history and cross-machine sync are just git.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Warn if .vars.local.yaml exists and is not covered by .gitignore.
		// Only relevant when running inside a project directory.
		if _, err := os.Stat(".vars.yaml"); err == nil {
			warnIfLocalNotGitignored()
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// First-time setup: no store yet — create it.
		if !vault.Exists(storeDir()) {
			if err := firstRun(storeDir()); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "\nYou're all set. Try:")
			fmt.Fprintln(os.Stderr, "  vars set MY_KEY     # store a value")
			fmt.Fprintln(os.Stderr, "  vars get MY_KEY     # retrieve it")
			fmt.Fprintln(os.Stderr, "  vars --help         # see all commands")
			return nil
		}
		return cmd.Help()
	},
}

// Execute runs the root command. Called from main.
func Execute() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("vars {{.Version}}\n")
	if err := rootCmd.Execute(); err != nil {
		// Determine exit code: ExitError for user errors (1), default 2
		if exitErr, ok := err.(*ExitError); ok {
			if exitErr.Message != "" { // empty message = caller already printed (e.g. git passthrough)
				fmt.Fprintln(os.Stderr, exitErr.Error())
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}
}

// ExitError is an error with a specific exit code.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	return "vars: " + e.Message
}

// UserError returns an ExitError with exit code 1 (user error).
func UserError(msg string) *ExitError {
	return &ExitError{Code: 1, Message: msg}
}

// InternalError returns an ExitError with exit code 2 (internal error).
func InternalError(msg string) *ExitError {
	return &ExitError{Code: 2, Message: msg}
}
