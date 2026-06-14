package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/vars-cli/vars/internal/prompt"
)

// stdinPrompt is a lazily-initialized Prompter backed by os.Stdin.
// All code must use this instead of prompt.New(os.Stdin, ...) to avoid
// creating multiple bufio.Readers over the same stdin.
var stdinPrompt *prompt.Prompter

func stdinPrompter() *prompt.Prompter {
	if stdinPrompt == nil {
		stdinPrompt = prompt.New(os.Stdin, os.Stderr)
	}
	return stdinPrompt
}

// printManifestHint prints a hint if .vars.yaml exists in cwd
// and the key is not listed in it. Strips scope prefix before checking
// so that "prod/RPC_URL" correctly matches "- RPC_URL" in the manifest.
func printManifestHint(key string) {
	data, err := os.ReadFile(".vars.yaml")
	if err != nil {
		return
	}
	bareKey := key
	if i := strings.IndexByte(key, '/'); i >= 0 {
		bareKey = key[i+1:]
	}
	if !containsKey(string(data), bareKey) {
		fmt.Fprintf(os.Stderr, "Hint: %q is not listed in .vars.yaml. Consider adding it.\n", key)
	}
}

// containsKey checks if a key appears as a YAML list item (- KEY).
func containsKey(yamlContent string, key string) bool {
	needle := "- " + key
	idx := strings.Index(yamlContent, needle)
	if idx < 0 {
		return false
	}
	// Ensure it's at end-of-string or followed by a newline (not a prefix of another key).
	end := idx + len(needle)
	return end == len(yamlContent) || yamlContent[end] == '\n' || yamlContent[end] == '\r'
}
