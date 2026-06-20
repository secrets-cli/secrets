package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/vars-cli/vars/internal/manifest"
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

// printManifestHint warns when key is not declared in .vars.yaml. It uses the
// manifest parser (not ad-hoc string matching), so quoting or spacing can't
// produce a false hint. The scope prefix is stripped first, so "prod/RPC_URL"
// matches a "RPC_URL" entry.
func printManifestHint(key string) {
	m, err := manifest.Load(".vars.yaml")
	if err != nil {
		return // no manifest in cwd (or unreadable): nothing to hint about
	}
	bareKey := key
	if i := strings.IndexByte(key, '/'); i >= 0 {
		bareKey = key[i+1:]
	}
	for _, k := range m.Keys {
		if k == bareKey {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "Hint: %q is not listed in .vars.yaml. Consider adding it.\n", key)
}

// preview renders a short, scrollback-safe glimpse of a secret for conflict
// prompts: enough to tell two values apart without echoing them. Values of 6
// characters or fewer are shown only as a length, never revealed.
func preview(s string) string {
	const n = 6
	r := []rune(s)
	if len(r) <= n {
		return fmt.Sprintf("(%d chars)", len(r))
	}
	return fmt.Sprintf("%s… (%d chars)", string(r[:n]), len(r))
}

// conflictAction is how the user chose to resolve an existing-key conflict.
type conflictAction int

const (
	actionSkip    conflictAction = iota // leave the stored value untouched
	actionReplace                       // overwrite with the new value
	actionRename                        // write under a different key (returned alongside)
)

// resolveConflict runs the interactive "key already exists" prompt and returns
// the chosen action. For actionRename it also returns the new key, which may
// itself already exist, so callers re-check. The caller prints the conflict
// banner before calling and acts on the result after; this only owns the choice,
// so it is testable with a Prompter over any reader. An empty or unrecognized
// answer is a safe skip.
func resolveConflict(p *prompt.Prompter) (conflictAction, string, error) {
	choice, err := p.Line("[r]eplace  [n]ew name  [s]kip > ")
	if err != nil {
		return actionSkip, "", err
	}
	switch c := strings.ToLower(strings.TrimSpace(choice)); {
	case strings.HasPrefix(c, "r"):
		return actionReplace, "", nil
	case strings.HasPrefix(c, "n"):
		newKey, err := p.Line("New key name (scopes allowed, e.g. prod/K): ")
		if err != nil {
			return actionSkip, "", err
		}
		if newKey = strings.TrimSpace(newKey); newKey != "" {
			return actionRename, newKey, nil
		}
		// empty name: fall through to skip
	}
	return actionSkip, "", nil
}

// uniqueStrings returns items with duplicates removed, preserving first-seen order.
func uniqueStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
