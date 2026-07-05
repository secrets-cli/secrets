# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [0.11.0]

### Added

Adding support for Homebrew.

## [0.10.0]

### Fixed
- Auto-`ssh-add` no longer misfires in non-interactive contexts. It now gates on **stdin** being a terminal (how `ssh-add` itself decides whether to prompt) and forces ssh-add to use the terminal via `SSH_ASKPASS_REQUIRE=never`, never a GUI askpass. Fixes a noisy `ssh_askpass: exec(/usr/X11R6/bin/ssh-askpass): No such file or directory` error on macOS when running `vars resolve` from a shell hook / `direnv` / piped stdin (stderr was a TTY but stdin wasn't); those contexts now get just the clean "load it with `ssh-add <path>`" message.

## [0.9.0]

### Added
- `vars info` prints a read-only summary of the store: its location, the SSH key it's encrypted to and whether that key is available right now (and from where, agent, a `~/.ssh` file, or `VARS_SSH_KEY`), the secret/scope counts, and local git state. It needs no key and touches no network, so it's the command to run when you can't decrypt and want to know why.

### Changed
- The SSH key is found by **fingerprint** across `~/.ssh`, not just the conventional `id_ed25519`/`id_rsa` names. So a dedicated decryption key under any filename (e.g. `~/.ssh/id_vars`) is picked up automatically, no `VARS_SSH_KEY` needed. When the matching key is found but can't be loaded (passphrase-protected and not in the agent), the error names the exact file: `load it with ssh-add <path>`.
- When a command needs the key and it isn't loaded, vars runs `ssh-add` on **that specific key** (prompting for its passphrase) and proceeds in one go, but only when there's a terminal to prompt on and an agent to load into. Non-interactive runs get a clean error instead of hanging, and an explicit `VARS_SSH_KEY` is left strict (not auto-loaded). Use `ssh -t host vars …` to get a prompt over SSH.
- `vars dump` confirms before printing every secret in plaintext (the deliberate exception to the store's purpose). `--force`/`-f` skips the prompt and is required for non-interactive use, so a stray script can't mass-export every secret. `vars resolve` remains the command for feeding secrets into a process/pipe.
- `vars sync` reports the remote it synced with (`Store synced with <remote>`); `clone` and `sync` sit together in `vars help`.

### Fixed
- `vars dump` fails once with a single message when the store's SSH key isn't available, instead of warning per key. The per-key skip+warn remains for individual unreadable files.

## [0.8.0]

### Added
- `vars clone <remote>` clones an existing store from a git remote into your local store directory (e.g. to set up from a store you already pushed), instead of creating a fresh, divergent one. It replaces an empty local store but refuses to overwrite one that holds secrets, locks the store directory to `0700`, and reports whether the SSH key the store is encrypted to is available (that key may differ from the one that authenticated the clone). `git clone` sets `origin`, so `vars sync` works immediately.

### Changed
- Writing to a store self-heals its static scaffolding: a missing `README.md`, `.gitignore`, or `.gitattributes` is recreated on the next write (a deleted `.gitignore` re-arms the default-deny allowlist before secrets are committed). Existing files are never overwritten. The scaffold now has a single source of truth in the `vault` package, shared by store creation and writes.
- `vars ls <arg>` accepts only a scope.
- Concurrent mutations are serialized by an advisory file lock (`flock` on a gitignored `.vars.lock`), so two simultaneous writes no longer race the git index, and a rename can't clobber a concurrently created key. The lock auto-releases if the process dies; it's a no-op on platforms without `flock` (vars targets Unix).
- Key names are restricted to `[A-Za-z0-9_-]` segments separated by `/`. This rejects accents and other non-ASCII (which collide across machines under Unicode normalization), control characters, and path-traversal, keeping keys portable and predictable.

### Fixed
- `vars set` treated an existing key whose value can't be decrypted (corrupt or foreign file, or the wrong key loaded) as a brand-new key: `--skip` would overwrite it and a plain `set` replaced it silently. It now surfaces the read failure instead, matching `vars import`.

## [0.7.0]

### Changed
- `vars history <key>` renamed to `vars log <key>`. It lists the key's committed states, each tagged with the `~N` you pass to `vars get <key>~N` (`~0` = latest, `~1` = before) instead of a commit hash, with local date+time. A commit that removed the key shows as `(removed)` (a state with no value); it needs no SSH key (reads git metadata only).
- `vars get <key>~N` is the key's state `N` commits back in its own history, not global git history (it always was per-key; the docs wrongly implied git's `HEAD~N`). If state `N` was a removal it has no value: nothing is printed and the command exits non-zero (a removed key's last value is then `~1`).
- The `[n]ew name` conflict option in `set`/`import` asks for a full key (scopes allowed, e.g. `prod/K`) instead of appending a `_<suffix>`.
- Key-free commands (`ls`, `scope`, `mv`, `rm`) no longer require the SSH key; it's resolved lazily, only when a command actually encrypts or decrypts.

## [0.6.0]

Complete re-architecture: from a single scrypt-encrypted blob + gRPC agent to a
per-file, SSH-encrypted, git-tracked store. **Breaking: there is no in-place
migration.** Export from the old binary and import into the new one (see README).

### Added
- Per-file `age` store: one encrypted `.age` file per secret, scopes as directories, rooted at a git repo.
- SSH-derived encryption (`ssh-v1` scheme): each file's key comes from a deterministic `SSHSIG` signature (namespace `vars.store.v1`) by your Ed25519/RSA key, via `ssh-agent` or the key file (no passphrase, no daemon).
- `vars get KEY~N`: retrieve the value N versions ago from git history.
- `vars history <key>`: a key's change history (newest first).
- `vars sync`: `pull --rebase` then push. `vars git <args>`: passthrough to git in the store dir.
- In-tree break-glass `README.md` documenting manual decryption with `ssh-keygen`; a default-deny `.gitignore`.
- `VARS_SSH_KEY` to pin a specific key; first-run key selection from `ssh-agent`.
- `vars set KEY -` reads the value from stdin verbatim, the way to store a multi-line secret such as a PEM; a non-interactive `set` with no value now errors instead of silently truncating to one line.
- The store ships a `.gitattributes` marking `*.age` binary, so git never text-merges ciphertext or rewrites its line endings.
- First run reports what was actually set up (no false "versioned with git" when git is absent), and when git is active it nudges `vars git remote add origin <url>` for an encrypted off-machine backup.

### Changed
- Encryption is now per-file age wrapped by an SSH-derived key (was age/scrypt of one blob).
- History and sync are git-native (`git log` = history, `git push`/`pull` = sync); git is a soft dependency.
- The store lives at `~/.local/share/vars/store/` (override with `VARS_STORE_DIR`); descriptor is `store.json`.
- `resolve` emits only valid shell variable names: it rejects unsafe manifest names and skips unsafe names piped from a `.env`, so the `eval`-able output can't be a shell-injection vector.
- `--dotenv` output (both `resolve` and `dump`) refuses a value containing a newline instead of emitting a broken `KEY=value` line.
- `dump` skips a file it cannot decrypt (warning + non-zero exit) instead of aborting on the first, so recovery returns everything readable.
- Conflict prompts in `set`/`import` mask secret values (a short preview, not the full secret).
- `vars sync` commits pending changes first and, on a conflicting rebase, aborts cleanly with guidance instead of leaving the repo mid-rebase.
- `vars git` mirrors git's real exit code instead of flattening it to 1.
- `import` parses large single-line values (was capped at 64 KB); `rm K K` de-duplicates; `mv K K` is rejected.

### Fixed
- `vars history`, `vars get KEY~N`, and the sync hint silently returned nothing due to a buffer-read-before-exec bug in the git runner.
- A passphrase-protected key file now returns a clear `ssh-add` hint instead of a raw parse error.

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
