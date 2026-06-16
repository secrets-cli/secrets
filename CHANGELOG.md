# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [0.6.0] (unreleased)

Complete re-architecture: from a single scrypt-encrypted blob + gRPC agent to a
per-file, SSH-encrypted, git-tracked store. **Breaking: there is no in-place
migration.** Export from the old binary and import into the new one (see README).

### Added
- Per-file `age` store: one encrypted `.age` file per secret, scopes as directories, rooted at a git repo.
- SSH-derived encryption (`ssh-v1` scheme): each file's key comes from a deterministic `SSHSIG` signature (namespace `vars.store.v1`) by your Ed25519/RSA key, via `ssh-agent` or the key file (no passphrase, no daemon).
- `vars get KEY~N`: retrieve the value N versions ago from git history.
- `vars history <key>`: the git commit log for one key.
- `vars sync`: `pull --rebase` then push. `vars git <args>`: passthrough to git in the store dir.
- In-tree break-glass `README.md` documenting manual decryption with `ssh-keygen`; a default-deny `.gitignore`.
- `VARS_SSH_KEY` to pin a specific key; first-run key selection from `ssh-agent`.
- `vars set KEY -` reads the value from stdin verbatim (the way to store a multi-line secret such as a PEM); a non-interactive `set` with no value now errors instead of silently truncating to one line.

### Changed
- Encryption is now per-file age wrapped by an SSH-derived key (was age/scrypt of one blob).
- History and sync are git-native (`git log` = history, `git push`/`pull` = sync); git is a soft dependency.
- The store lives at `~/.local/share/vars/store/` (override with `VARS_STORE_DIR`); descriptor is `store.json`.
- `resolve` emits only valid shell variable names (it rejects such manifest names and skips piped `.env` ones), so its `eval`-able output can't be a shell-injection vector; `--dotenv` errors on a value containing a newline rather than emitting a broken line.

### Removed
- The gRPC agent (`vars agent`), the scrypt single-blob store, the passphrase machinery (`vars passwd`, passphrase re-checks, `VARS_AGENT_TTL`/`SOCK`), and the protobuf/grpc dependencies.
- The `--stdin` flow (CI uses env injection + `resolve`'s shell fallback).

## [0.5.0]

### Added
- `vars mv --force` / `-f` — skip confirmation prompt for non-interactive use (CI / scripts)
- CI and non-interactive usage documented: `--replace` on `set`/`import`, `--force` on `rm`/`mv`, `echo "" | vars agent --stdin` for stores with no passphrase

### Changed
- Passphrase re-confirmation removed from `set --replace`, `rm`, and `mv` — the active agent session is the authentication boundary; asking for the passphrase again was redundant given that `vars dump` is always available to a session holder
- `rm` without `--force` now shows a summary and prompts `Confirm? [y/N]` instead of asking for the passphrase; `--force` skips both
- `mv` without `--force` now prompts `Confirm? [y/N]`; in non-TTY mode it errors with a clear message directing to `--force`
- `set` and `import` write directly after conflict resolution — no passphrase step

### Removed
- `passphrase` field removed from `SetRequest`, `DeleteRequest`, and `RenameRequest` proto messages — protocol simplified; `PasswdRequest` retains it (re-encryption requires the current credential)

## [0.4.0]

### Added
- `vars init` scaffolds a `.vars.yaml` manifest with commented examples in the current directory
- `vars resolve` shell env fallback — if a manifest key is absent from the store and any piped `.env`, the current shell environment is used as a last resort; no export emitted, no error thrown; `--origin` annotates it as `# shell`
- `vars agent --stdin` — reads the store passphrase from stdin for non-interactive startup (CI / script flows where stdin is already occupied by a piped dotenv file)
- `?= value` default syntax in profile entries — uses the store value when present and non-empty, otherwise emits the default; follows the same trimming and quote-stripping rules as `= value`
- `.vars.local.yaml` gitignore warning — any command run inside a project directory warns on stderr if `.vars.local.yaml` exists but is not covered by `.gitignore`
- `--origin` now annotates `= value` and `?= value` entries as `# manifest`; shell-environment values as `# shell` (no export line emitted); missing keys as `# KEY  missing` (only with `--partial`)

### Changed
- `mappings:` key removed from `.vars.yaml` and `.vars.local.yaml` — team-wide aliases now live in the reserved `profiles: global:` entry, which is always applied as a fallback regardless of the active profile; `--profile global` is an error
- `--force` renamed to `--replace` on `set` and `import` (signals intent: replace an existing value); `rm --force` unchanged (skip-confirmation semantics)
- Interactive conflict prompt changed from `[o]verwrite  [r]ename  [s]kip` to `[r]eplace  [n]ew name  [s]kip`
- `--origin` source label `stdin` renamed to `.env`; `literal` and `default` unified into `manifest`
- `--dotenv` output is now bare `KEY=value` with no quoting — compatible with `docker --env-file` and tools that read values literally
- "overwrite" replaced with "replace" throughout all user-facing messages, flags, and documentation

## [0.3.0]

- `vars` with no arguments triggers a first-run setup wizard when no store exists: explains store location, prompts for passphrase, creates the store, starts the agent, and prints next steps
- `VARS_AGENT_TTL` environment variable sets the default agent lifetime (e.g. `export VARS_AGENT_TTL=4h` in your shell profile); falls back to 8 hours if unset
- `vars resolve --origin` appends an inline `# vars`, `# stdin`, or `# KEY  not set` comment to each output line — eval-safe across all output formats, useful for auditing which source each value came from
- `vars resolve -p <profile>` warns on stderr when the named profile does not exist in the manifest
- Profile entries starting with `=` resolve to inline literal values instead of store keys (e.g. `LOG_LEVEL: =info`)
- `vars resolve` with stdin piped now exits with an error if the agent is not running, rather than consuming stdin through the passphrase prompt
- All user-facing error messages are now prefixed with `vars:` for clarity when the tool is embedded in scripts or pipelines

## [0.2.0]

### Added
- `vars resolve` merges stdin dotenv as a fallback source — store values take priority for manifest keys; dotenv acts as fallback for keys not yet in the store; non-manifest keys pass through unchanged
- Agent is now the exclusive write gateway — all writes (`set`, `rm`, `mv`, `import`, `passwd`) go through the agent and are persisted to disk immediately

### Changed
- Project renamed from `secrets` to `vars` (binary name, env vars `VARS_STORE_DIR` / `VARS_AGENT_SOCK`, store path `~/.local/share/vars/`, manifest files `.vars.yaml` / `.vars.local.yaml`)
- `vars init` removed — the first command that needs the store creates it transparently with a passphrase prompt
- `--overwrite` flag renamed to `--force` on `set` and `import`, consistent with `rm`
- `vars passwd` now prompts for the current passphrase first, then the new passphrase (previously prompted new passphrase first)
- `vars history <key>` now errors if the key does not exist, instead of printing nothing
- Error messages standardised: lowercase, no trailing period
- Batch Set and Delete RPCs — `import` and multi-key `rm` run a single scrypt encryption call regardless of how many keys are affected, significantly reducing write latency

---

## [0.1.0]

### Added
- Encrypted secret store using age/scrypt (`vars init`, `vars set`, `vars get`, `vars ls`, `vars rm`)
- Passphrase management (`vars passwd`) with empty passphrase support
- Per-project manifests (`.vars.yaml`) with export to posix, fish, and dotenv formats
- Per-developer remapping via `.vars-map.yaml`
- `--partial` flag for resolve: skip missing keys instead of erroring
- Background agent (`vars agent`) holding decrypted store in memory with configurable TTL
- Agent is read-only: serves get/list over Unix domain socket
- Trial-decrypt for empty passphrases (no marker files, like OpenSSH)
- Pluggable `crypto.Backend` interface for future Yubikey/SSH agent support
- Atomic writes (temp file + rename) for crash safety
- Memory zeroing of decrypted secrets on close
- Permission checking with actionable fix commands
- XDG-compliant store location (`~/.local/share/vars/`)
- `VARS_STORE_DIR` environment variable override
- GitHub Actions CI (vet, test, cross-compile) and release workflows
- goreleaser configuration for 5-target builds
- Comprehensive test suite: 70+ unit tests, 22 integration tests, smoke test
