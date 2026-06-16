package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	return path
}

// --- Load ---

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `
keys:
  - PRIVATE_KEY
  - RPC_URL
  - ETHERSCAN_API
profiles:
  global:
    PRIVATE_KEY: prod/PRIVATE_KEY
  mainnet:
    RPC_URL: prod/RPC_URL
  sepolia:
    RPC_URL: sepolia/RPC_URL
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Keys) != 3 {
		t.Fatalf("Keys len = %d, want 3", len(m.Keys))
	}
	if m.Profiles["global"]["PRIVATE_KEY"] != "prod/PRIVATE_KEY" {
		t.Fatalf("Profiles[global][PRIVATE_KEY] = %q", m.Profiles["global"]["PRIVATE_KEY"])
	}
	if m.Profiles["mainnet"]["RPC_URL"] != "prod/RPC_URL" {
		t.Fatalf("Profiles[mainnet][RPC_URL] = %q", m.Profiles["mainnet"]["RPC_URL"])
	}
	if m.Profiles["sepolia"]["RPC_URL"] != "sepolia/RPC_URL" {
		t.Fatalf("Profiles[sepolia][RPC_URL] = %q", m.Profiles["sepolia"]["RPC_URL"])
	}
}

func TestLoad_KeysOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
  - BAR
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Keys) != 2 {
		t.Fatalf("Keys len = %d, want 2", len(m.Keys))
	}
	if len(m.Profiles) != 0 {
		t.Fatalf("Profiles should be empty, got %v", m.Profiles)
	}
}

func TestLoad_DuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
  - BAR
  - FOO
  - BAR
  - BAZ
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Keys) != 3 {
		t.Fatalf("Keys len = %d, want 3 (deduped)", len(m.Keys))
	}
	expected := []string{"FOO", "BAR", "BAZ"}
	for i, k := range m.Keys {
		if k != expected[i] {
			t.Fatalf("Keys[%d] = %q, want %q", i, k, expected[i])
		}
	}
}

func TestLoad_EmptyKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `keys: []`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Keys) != 0 {
		t.Fatalf("Keys len = %d, want 0", len(m.Keys))
	}
}

func TestLoad_NotFound(t *testing.T) {
	_, err := Load("/nonexistent/.vars.yaml")
	if err == nil {
		t.Fatal("Load nonexistent should fail")
	}
	if !strings.Contains(err.Error(), "manifest not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `keys: [[[invalid`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("invalid YAML should fail")
	}
}

func TestLoad_UnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
unknown_field: something
another: 42
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load with unknown fields: %v", err)
	}
	if len(m.Keys) != 1 {
		t.Fatalf("Keys len = %d, want 1", len(m.Keys))
	}
}

// --- LoadLocal ---

func TestLoadLocal_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.local.yaml", `
profiles:
  global:
    PRIVATE_KEY: prod/PRIVATE_KEY_alice
  mainnet:
    RPC_URL: prod/RPC_URL_quicknode
`)

	local, err := LoadLocal(path)
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if local.Profiles["global"]["PRIVATE_KEY"] != "prod/PRIVATE_KEY_alice" {
		t.Fatalf("Profiles[global][PRIVATE_KEY] = %q", local.Profiles["global"]["PRIVATE_KEY"])
	}
	if local.Profiles["mainnet"]["RPC_URL"] != "prod/RPC_URL_quicknode" {
		t.Fatalf("Profiles[mainnet][RPC_URL] = %q", local.Profiles["mainnet"]["RPC_URL"])
	}
}

func TestLoadLocal_NotFound(t *testing.T) {
	local, err := LoadLocal("/nonexistent/.vars.local.yaml")
	if err != nil {
		t.Fatalf("LoadLocal nonexistent should not error: %v", err)
	}
	if local.Profiles != nil {
		t.Fatalf("LoadLocal nonexistent should return empty, got %+v", local)
	}
}

func TestLoadLocal_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".vars.local.yaml", `[[[invalid`)

	_, err := LoadLocal(path)
	if err == nil {
		t.Fatal("invalid YAML should fail")
	}
}

// --- Resolve ---

func TestResolve_NoProfiles(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
  - BAR
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(vars) != 2 {
		t.Fatalf("vars len = %d, want 2", len(vars))
	}
	for _, v := range vars {
		if v.EnvName != v.StoreKey {
			t.Fatalf("without profiles, EnvName %q should equal StoreKey %q", v.EnvName, v.StoreKey)
		}
	}
}

func TestResolve_GlobalProfile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
  - BAR
  - BAZ
profiles:
  global:
    FOO: FOO_remapped
    BAZ: BAZ_personal
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	expected := map[string]string{
		"FOO": "FOO_remapped",
		"BAR": "BAR", // not in global → identity
		"BAZ": "BAZ_personal",
	}
	for _, v := range vars {
		want := expected[v.EnvName]
		if v.StoreKey != want {
			t.Fatalf("StoreKey for %q = %q, want %q", v.EnvName, v.StoreKey, want)
		}
	}
}

func TestResolve_LocalGlobalOverride(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
profiles:
  global:
    FOO: FOO_team
`)
	localPath := writeFile(t, dir, ".vars.local.yaml", `
profiles:
  global:
    FOO: FOO_alice
`)

	vars, _, err := Resolve(manifestPath, localPath, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if vars[0].StoreKey != "FOO_alice" {
		t.Fatalf("local global should override committed global: got %q", vars[0].StoreKey)
	}
}

func TestResolve_Profile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - PRIVATE_KEY
  - RPC_URL
  - ETHERSCAN_API
profiles:
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY
    RPC_URL: prod/RPC_URL
  sepolia:
    PRIVATE_KEY: test/PRIVATE_KEY
    RPC_URL: sepolia/RPC_URL
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "mainnet")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	expected := map[string]string{
		"PRIVATE_KEY":   "prod/PRIVATE_KEY",
		"RPC_URL":       "prod/RPC_URL",
		"ETHERSCAN_API": "ETHERSCAN_API", // not in profile → identity
	}
	for _, v := range vars {
		want := expected[v.EnvName]
		if v.StoreKey != want {
			t.Fatalf("StoreKey for %q = %q, want %q", v.EnvName, v.StoreKey, want)
		}
	}
}

func TestResolve_LocalProfileOverride(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - PRIVATE_KEY
profiles:
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY_team
`)
	localPath := writeFile(t, dir, ".vars.local.yaml", `
profiles:
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY_alice
`)

	vars, _, err := Resolve(manifestPath, localPath, "mainnet")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if vars[0].StoreKey != "prod/PRIVATE_KEY_alice" {
		t.Fatalf("local profile should override committed: got %q", vars[0].StoreKey)
	}
}

func TestResolve_ProfileFallbackToGlobal(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - PRIVATE_KEY
  - ETHERSCAN_API
profiles:
  global:
    ETHERSCAN_API: ETHERSCAN_API_v2
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "mainnet")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	expected := map[string]string{
		"PRIVATE_KEY":   "prod/PRIVATE_KEY",
		"ETHERSCAN_API": "ETHERSCAN_API_v2", // not in profile → falls back to global
	}
	for _, v := range vars {
		want := expected[v.EnvName]
		if v.StoreKey != want {
			t.Fatalf("StoreKey for %q = %q, want %q", v.EnvName, v.StoreKey, want)
		}
	}
}

func TestResolve_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - CHARLIE
  - ALPHA
  - BRAVO
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	expected := []string{"CHARLIE", "ALPHA", "BRAVO"}
	for i, v := range vars {
		if v.EnvName != expected[i] {
			t.Fatalf("vars[%d].EnvName = %q, want %q", i, v.EnvName, expected[i])
		}
	}
}

func TestResolve_UnknownProfile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
`)

	vars, profileFound, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "nonexistent")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profileFound {
		t.Fatal("expected profileFound=false for unknown profile")
	}
	if vars[0].StoreKey != "FOO" {
		t.Fatalf("unknown profile should fall through to identity: got %q", vars[0].StoreKey)
	}
}

func TestResolve_GlobalProfileReserved(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - FOO
`)

	_, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "global")
	if err == nil {
		t.Fatal("selecting global profile should error")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved error, got: %v", err)
	}
}

func TestResolve_DefaultProfileAutoApplied(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - RPC_URL
profiles:
  default:
    RPC_URL: prod/RPC_URL
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if vars[0].StoreKey != "prod/RPC_URL" {
		t.Fatalf("default profile should be auto-applied: got %q", vars[0].StoreKey)
	}
}

func TestResolve_DefaultProfileInLocalAutoApplied(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - RPC_URL
`)
	writeFile(t, dir, ".vars.local.yaml", `
profiles:
  default:
    RPC_URL: local/RPC_URL
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if vars[0].StoreKey != "local/RPC_URL" {
		t.Fatalf("default profile in local file should be auto-applied: got %q", vars[0].StoreKey)
	}
}

func TestResolve_InlineLiteralValue(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - LOG_LEVEL
  - API_KEY
profiles:
  ci:
    LOG_LEVEL: = info
    API_KEY: ci/API_KEY
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "ci")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, v := range vars {
		switch v.EnvName {
		case "LOG_LEVEL":
			if !v.IsInline {
				t.Fatal("LOG_LEVEL should be inline")
			}
			if v.InlineValue != "info" {
				t.Fatalf("InlineValue = %q, want \"info\"", v.InlineValue)
			}
		case "API_KEY":
			if v.IsInline {
				t.Fatal("API_KEY should not be inline")
			}
			if v.StoreKey != "ci/API_KEY" {
				t.Fatalf("StoreKey = %q, want \"ci/API_KEY\"", v.StoreKey)
			}
		}
	}
}

func TestResolve_InlineLiteralEmptyValue(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - DRY_RUN
profiles:
  test:
    DRY_RUN: =
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !vars[0].IsInline {
		t.Fatal("DRY_RUN should be inline")
	}
	if vars[0].InlineValue != "" {
		t.Fatalf("InlineValue = %q, want \"\"", vars[0].InlineValue)
	}
}

func TestResolve_InlineLiteralInGlobal(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - LOG_LEVEL
  - API_KEY
profiles:
  global:
    LOG_LEVEL: = info
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, v := range vars {
		switch v.EnvName {
		case "LOG_LEVEL":
			if !v.IsInline {
				t.Fatal("LOG_LEVEL should be inline")
			}
			if v.InlineValue != "info" {
				t.Fatalf("InlineValue = %q, want \"info\"", v.InlineValue)
			}
		case "API_KEY":
			if v.IsInline {
				t.Fatal("API_KEY should not be inline")
			}
			if v.StoreKey != "API_KEY" {
				t.Fatalf("StoreKey = %q, want \"API_KEY\"", v.StoreKey)
			}
		}
	}
}

func TestResolve_DefaultValue(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - RPC_URL
  - API_KEY
profiles:
  global:
    RPC_URL: ?= http://localhost:8545
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, v := range vars {
		switch v.EnvName {
		case "RPC_URL":
			if !v.HasDefault {
				t.Fatal("RPC_URL should have HasDefault=true")
			}
			if v.DefaultValue != "http://localhost:8545" {
				t.Fatalf("DefaultValue = %q, want %q", v.DefaultValue, "http://localhost:8545")
			}
			if v.StoreKey != "RPC_URL" {
				t.Fatalf("StoreKey = %q, want %q", v.StoreKey, "RPC_URL")
			}
		case "API_KEY":
			if v.HasDefault {
				t.Fatal("API_KEY should not have HasDefault")
			}
		}
	}
}

func TestResolve_DefaultValueInProfile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeFile(t, dir, ".vars.yaml", `
keys:
  - DRY_RUN
profiles:
  ci:
    DRY_RUN: ?= false
`)

	vars, _, err := Resolve(manifestPath, filepath.Join(dir, ".vars.local.yaml"), "ci")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !vars[0].HasDefault {
		t.Fatal("DRY_RUN should have HasDefault=true")
	}
	if vars[0].DefaultValue != "false" {
		t.Fatalf("DefaultValue = %q, want \"false\"", vars[0].DefaultValue)
	}
}

func TestParseInlineValue(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{" hello", "hello"},
		{"  hello world", "hello world"},
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{` "hello"`, "hello"},
		{`"it's fine"`, "it's fine"},
		{`"hello \"world\""`, `hello \"world\"`}, // only outer pair stripped
		{"", ""},
		{`""`, ""},
	}
	for _, tc := range cases {
		got := parseInlineValue(tc.input)
		if got != tc.want {
			t.Errorf("parseInlineValue(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
