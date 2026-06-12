package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vars-cli/vars/internal/agent"
	"github.com/vars-cli/vars/internal/envfile"
	"github.com/vars-cli/vars/internal/format"
	"github.com/vars-cli/vars/internal/manifest"
)

var (
	resolveFish    bool
	resolveDotenv  bool
	resolveFile    string
	resolvePartial bool
	resolveProfile string
	resolveOrigin  bool
)

func init() {
	resolveCmd.Flags().BoolVar(&resolveDotenv, "dotenv", false, "Output as KEY=value")
	resolveCmd.Flags().BoolVar(&resolveFish, "fish", false, "Output in fish shell format")
	resolveCmd.Flags().StringVarP(&resolveFile, "file", "f", ".vars.yaml", "Path to the manifest file")
	resolveCmd.Flags().BoolVar(&resolvePartial, "partial", false, "Skip missing keys instead of erroring")
	resolveCmd.Flags().StringVarP(&resolveProfile, "profile", "p", "", "Active profile name")
	resolveCmd.Flags().BoolVar(&resolveOrigin, "origin", false, "Annotate each line with its source (vars, .env, manifest, shell, missing)")
	rootCmd.AddCommand(resolveCmd)
}

var resolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolve manifest keys and print as shell variables",
	Long: `Read .vars.yaml, resolve all variables against the store, and print as
shell-source-able lines to stdout.

  eval "$(vars resolve)"
  vars resolve --profile mainnet
  cat .env | vars resolve --partial

When stdin is a dotenv file, it is used as a fallback for missing store keys.
Non-manifest keys from stdin are passed through unchanged.
Store values always take priority over stdin values.

Resolution priority (per key):
  1. Active profile from .vars.local.yaml (personal override)
  2. Active profile from .vars.yaml
  3. global: profile from .vars.local.yaml
  4. global: profile from .vars.yaml
  5. Bare key (identity)

Mapping values may use special prefixes:
  = value     emit literal value, no store lookup  (origin: manifest)
  ?= value    use store value; fall back to default (origin: manifest when default used)

--origin sources: vars | .env | manifest | shell | missing`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		formatter := format.Posix
		if resolveFish {
			formatter = format.Fish
		} else if resolveDotenv {
			formatter = format.Dotenv
		}

		localPath := filepath.Join(filepath.Dir(resolveFile), ".vars.local.yaml")
		vars, profileFound, err := manifest.Resolve(resolveFile, localPath, resolveProfile)
		if err != nil {
			return UserError(err.Error())
		}
		if !profileFound {
			fmt.Fprintf(os.Stderr, "vars: warning: profile %q not found in manifest\n", resolveProfile)
		}

		sockPath := agentSocketPath()

		// Check stdin pipe before touching the agent — if stdin is piped, we
		// cannot prompt interactively, so fail fast if the agent isn't already up.
		stdinPiped := func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice == 0
		}()

		if stdinPiped && !agent.IsRunning(sockPath) {
			return UserError("agent is not running; start it first with `vars agent`")
		}

		if err := ensureAgent(); err != nil {
			return err
		}

		// Parse stdin dotenv if piped
		var stdinEntries []envfile.Entry
		var stdinMap map[string]string
		if stdinPiped {
			var parseErr error
			stdinEntries, parseErr = envfile.Parse(os.Stdin)
			if parseErr != nil {
				return UserError("failed to parse stdin as dotenv: " + parseErr.Error())
			}
			stdinMap = make(map[string]string, len(stdinEntries))
			for _, e := range stdinEntries {
				stdinMap[e.Key] = e.Value
			}
		}

		// Build set of manifest env names to exclude them from pass-through
		manifestKeys := make(map[string]bool, len(vars))
		for _, v := range vars {
			manifestKeys[v.EnvName] = true
		}

		type entry struct {
			envName string
			value   string
			source  string // "vars" | ".env" | "manifest" | "shell" | "missing" | "" (pass-through)
		}
		var entries []entry

		// Resolve manifest keys: inline literals, then store, then .env/env fallbacks
		for _, v := range vars {
			if v.IsInline {
				entries = append(entries, entry{v.EnvName, v.InlineValue, "manifest"})
				continue
			}
			val, lookupErr := resolveStoreKey(sockPath, v.StoreKey)
			if v.HasDefault && (lookupErr != nil || val == "") {
				entries = append(entries, entry{v.EnvName, v.DefaultValue, "manifest"})
				continue
			}
			if lookupErr != nil {
				if dotval, ok := stdinMap[v.EnvName]; ok {
					entries = append(entries, entry{v.EnvName, dotval, ".env"})
					continue
				}
				if envval := os.Getenv(v.EnvName); envval != "" {
					entries = append(entries, entry{v.EnvName, envval, "shell"})
					continue
				}
				if resolvePartial {
					if resolveOrigin {
						entries = append(entries, entry{v.EnvName, "", "missing"})
					} else {
						fmt.Fprintf(os.Stderr, "vars: %q not found (skipping)\n", v.StoreKey)
					}
					continue
				}
				if v.StoreKey == v.EnvName {
					if hint := resolveProfileHint(v.EnvName, resolveFile, localPath, resolveProfile); hint != "" {
						return UserError(hint)
					}
					return UserError(fmt.Sprintf("key %q not found in store", v.EnvName))
				}
				return UserError(fmt.Sprintf("key %q not found in store (mapped from %q)", v.StoreKey, v.EnvName))
			}
			entries = append(entries, entry{v.EnvName, val, "vars"})
		}

		// Pass through stdin dotenv keys not declared in the manifest
		for _, e := range stdinEntries {
			if !manifestKeys[e.Key] {
				entries = append(entries, entry{e.Key, e.Value, ""})
			}
		}

		for _, e := range entries {
			switch e.source {
			case "missing":
				fmt.Fprintf(os.Stdout, "# %s  missing\n", e.envName)
			case "shell":
				// Value already present in the calling shell — no export needed.
				if resolveOrigin {
					fmt.Fprintf(os.Stdout, "# %s  shell\n", e.envName)
				}
			default:
				if resolveOrigin && e.source != "" {
					fmt.Fprintf(os.Stdout, "%s  # %s\n", formatter(e.envName, e.value), e.source)
				} else {
					fmt.Fprintln(os.Stdout, formatter(e.envName, e.value))
				}
			}
		}

		return nil
	},
}

// resolveProfileHint scans all profiles in the manifest and local manifest for
// any mapping of envName. When found (and no profile is currently active), it
// returns a user-friendly error hint explaining which profile(s) can resolve the key.
// Returns "" when no hint is applicable. Loads the manifests itself: this runs
// only on the key-not-found error path, so the parse cost is never on the happy path.
func resolveProfileHint(envName, manifestPath, localPath, activeProfile string) string {
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return ""
	}
	local, err := manifest.LoadLocal(localPath)
	if err != nil {
		return ""
	}

	type mapping struct {
		profile  string
		storeKey string
	}
	var mappings []mapping

	// Collect mappings from all non-global profiles in both manifests.
	// Skip the currently active profile since its mappings are already being used.
	for name, pm := range m.Profiles {
		if name == "global" || name == activeProfile {
			continue
		}
		if sk, ok := pm[envName]; ok {
			mappings = append(mappings, mapping{name, sk})
		}
	}
	for name, pm := range local.Profiles {
		if name == "global" || name == activeProfile {
			continue
		}
		if sk, ok := pm[envName]; ok {
			mappings = append(mappings, mapping{name, sk})
		}
	}

	if len(mappings) == 0 {
		return ""
	}

	// Build the hint message.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("key %q not found in store\n\n", envName))

	if len(mappings) == 1 {
		m := mappings[0]
		sb.WriteString(fmt.Sprintf("Hint: profile %q maps %s → %s\n", m.profile, envName, m.storeKey))
		if activeProfile == "" {
			sb.WriteString(fmt.Sprintf("Try: vars resolve --profile %s\n", m.profile))
		}
	} else {
		sb.WriteString("Hint: these profiles map " + envName + " to a store key:\n")
		for _, m := range mappings {
			sb.WriteString(fmt.Sprintf("  - %s → %s\n", m.profile, m.storeKey))
		}
		if activeProfile == "" {
			sb.WriteString("\nTry: vars resolve --profile <profile-name>\n")
		}
	}

	// List all available profiles (including global and active if present).
	var allProfiles []string
	for name := range m.Profiles {
		allProfiles = append(allProfiles, name)
	}
	for name := range local.Profiles {
		found := false
		for _, p := range allProfiles {
			if p == name {
				found = true
				break
			}
		}
		if !found {
			allProfiles = append(allProfiles, name)
		}
	}
	if len(allProfiles) > 0 {
		sb.WriteString("Available profiles: " + strings.Join(allProfiles, ", "))
	}

	return sb.String()
}

// resolveStoreKey tries the given key, then falls back by stripping the deepest
// scope one level at a time: "main/dev/RPC_URL" → "main/RPC_URL" → "RPC_URL".
// The deepest (leaf-adjacent) scope is dropped first so outer scopes act as the
// broader fallback, matching the documented hierarchical semantics.
func resolveStoreKey(sockPath, key string) (string, error) {
	for {
		val, err := agent.Get(sockPath, key)
		if err == nil {
			return val, nil
		}
		// Drop the scope segment immediately before the leaf key name.
		last := strings.LastIndexByte(key, '/')
		if last < 0 {
			return "", err // no scope left to strip
		}
		prev := strings.LastIndexByte(key[:last], '/')
		key = key[:prev+1] + key[last+1:]
	}
}
