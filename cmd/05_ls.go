package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(lsCmd)
}

var lsCmd = &cobra.Command{
	Use:   "ls [scope]",
	Short: "List keys in the store as a tree",
	Long: `Print the store's keys as a tree (scopes are directories).

With a scope argument, show only that subtree.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		keys, err := v.List()
		if err != nil {
			return InternalError(err.Error())
		}
		if len(args) == 1 {
			prefix := strings.TrimSuffix(args[0], "/") + "/"
			var sub []string
			for _, k := range keys {
				if strings.HasPrefix(k, prefix) {
					sub = append(sub, strings.TrimPrefix(k, prefix))
				}
			}
			keys = sub
		}
		printTree(os.Stdout, keys)
		return nil
	},
}

// printTree prints sorted, slash-delimited keys as an indented tree, with each
// scope directory shown once.
func printTree(w io.Writer, keys []string) {
	var prevDirs []string
	for _, key := range keys {
		parts := strings.Split(key, "/")
		dirs, leaf := parts[:len(parts)-1], parts[len(parts)-1]
		common := 0
		for common < len(dirs) && common < len(prevDirs) && dirs[common] == prevDirs[common] {
			common++
		}
		for d := common; d < len(dirs); d++ {
			fmt.Fprintf(w, "%s%s/\n", strings.Repeat("  ", d), dirs[d])
		}
		fmt.Fprintf(w, "%s%s\n", strings.Repeat("  ", len(dirs)), leaf)
		prevDirs = dirs
	}
}
