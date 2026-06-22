# Key Vars

**One encrypted home for all your project secrets, unlocked by the SSH key you already have.**

`.env` files are messy: they scatter across repos, sneak into commits, drift
between your laptop and your server, and sit in plaintext where any script (or
coding agent) can read them.

`vars` replaces them with a personal, encrypted store, and loads the
right secrets into any project on demand:

```sh
vars set DATABASE_URL "postgres://…"   # save a secret, encrypted with your SSH key
eval "$(vars resolve)"                 # load this project's secrets into the environment
deno run index.ts                      # your app just sees normal env vars
```

- **No `.env` files in your repos**: nothing to leak, nothing to gitignore.
- **No passphrase to remember**: your SSH key/agent unlocks it.
- **Synced and versioned**: your vars store is a git repo.
- **Built for teams**: list the env vars that a project needs and vars resolves them.

---

## Quick start

**1. Install**

```sh
# macOS (Apple Silicon)
curl -L https://github.com/vars-cli/vars/releases/latest/download/vars_darwin_arm64.tar.gz | tar xz
sudo mv vars /usr/local/bin/

# Linux / WSL (amd64)
curl -L https://github.com/vars-cli/vars/releases/latest/download/vars_linux_amd64.tar.gz | tar xz
sudo mv vars /usr/local/bin/
```

**2. Save a secret** (the first run creates your store automatically)

```sh
vars set RPC_URL "https://rpc.example.com"
vars set PRIVATE_KEY          # prompts for the value (masked), kept out of shell history
```

**3. Use it in a project**

```sh
vars init                     # scaffolds .vars.yaml in your project: list your keys here
eval "$(vars resolve)"        # bash/zsh: load the keys as env vars
vars resolve --fish | source  # fish
```

That's the main flow. The rest is optional depth.

> **Only one requirement: an SSH key**, the kind you already use for GitHub or a
> server.
>
> `vars` derives each file's encryption from SSH. Most people already have an
> SSH key at `~/.ssh/id_ed25519`; if not, create it with `ssh-keygen -t ed25519`.
> It must be **Ed25519 or RSA**.
>
> If your key has a passphrase, load it into your agent with `ssh-add`: vars signs
> through `ssh-agent`, so you're never asked for the passphrase.
> 
> Git is optional, used for history and cross-machine sync.

---

## Your personal store

```sh
vars set DB_URL "http://user@server/db"
vars set API_TOKEN               # prompts for the value (masked)
vars set TLS_KEY - < key.pem     # read the value from stdin (for multi-line: PEM, JSON, …)
vars get DB_URL                  # print the value
vars ls                          # list keys as a tree
vars rm API_TOKEN                # delete (use --force to skip confirmation)
vars mv OLD NEW                  # rename the key
vars dump                        # print everything (debugging / migration)
```

The store lives at `~/.local/share/vars/store/` by default (override with
`VARS_STORE_DIR`). It's just a directory of encrypted `.age` files with optional versioning.

Key names use letters, digits, `_` and `-`, with `/` for scopes (e.g. `prod/DB_URL`).
Other characters are rejected, so keys stay portable across machines and filesystems.

---

## Scopes

As your store grows, group related keys with `/`-delimited scopes. They behave like directories and show up as a tree:

```sh
vars set prod/PRIVATE_KEY "0xPROD"
vars set dev/PRIVATE_KEY  "0xDEV"
vars set dev/temp/RPC_URL "http://localhost:8545"   # nested

vars ls            # tree of everything
vars ls dev        # just the dev/ subtree
vars scope ls      # list scope prefixes (dev, dev/temp, prod)
```

---

## Using in a project

Multiple users can work on the same project despite each having a different store. 
Add a committed `.vars.yaml` declaring the env vars the project uses:

```yaml
# .vars.yaml
keys:
  - DB_URL
  - API_TOKEN
  - PRIVATE_KEY
```

`vars resolve` reads it, looks each key up in your store, and prints shell-ready
exports: nothing is written to disk.

```sh
# Loading to your shell
eval "$(vars resolve)"
./my-script

# Passing as an ephemeral file
docker run --env-file <(vars resolve --dotenv) my-image   # docker, no temp file
```

### Profiles

Profiles are named `env var → store key` mappings in `.vars.yaml`. They tell
`resolve` which scope/key to use per run.

```yaml
keys:
  - PRIVATE_KEY
  - RPC_URL
  - SERVER_API_KEY
profiles:
  global:                              # always applied as a fallback
    SERVER_API_KEY: SERVER_API_KEY_v2
  default:                             # auto-applied when no --profile is given
    PRIVATE_KEY: dev/PRIVATE_KEY
    RPC_URL: sepolia/RPC_URL
  mainnet:
    PRIVATE_KEY: prod/PRIVATE_KEY
    RPC_URL: mainnet/RPC_URL
```

```sh
vars resolve                # "default" profile applied automatically
vars resolve -p mainnet     # use the mainnet profile
```

### Scope fallback

When resolving a key, vars tries the most specific store key first, then strips
the deepest scope one level at a time:

```
main/dev/RPC_URL  →  main/RPC_URL  →  RPC_URL  →  not found
```

### Inline values

`= value` always emits that literal; `?= value` uses the store
value if present, else the default.

```yaml
profiles:
  global:
    LOG_LEVEL: = info                  # always "info", no store lookup
    RPC_URL: ?= http://localhost:8545  # store wins; falls back to localhost
```

### Local overrides

`.vars.local.yaml` (git-ignored) allows each user to override `.vars.yaml`:
your local deviations from the team's convention.

### Incremental adoption

`vars resolve` can work with existing env variables and dotenv files, using them as a fallback.

Pipe a `.env` in: the store values win for keys listed on the manifest, the dotenv fills the gaps,
and non-manifest keys pass through. If a key is missing from both, the current
shell env is the last fallback.

```sh
cat .env | vars resolve --partial          # skip keys missing everywhere
cat .env | vars resolve --partial --origin # annotate where each value came from
```

| Origin | Meaning |
|--------|---------|
| `vars` | from the encrypted store |
| `.env` | from piped stdin |
| `manifest` | inline `= value` / `?= default` |
| `shell` | already in the calling shell (no export emitted) |
| `missing` | not found anywhere (only with `--partial`) |

---

## History and versions (git)

When the store is a git repo, every change is committed automatically.

```sh
vars log RPC_URL            # this key's committed states, newest first, tagged ~0, ~1, …
vars get RPC_URL~1          # the previous value of this key
vars get RPC_URL~2          # two states back (counts only commits to this key)
vars git remote add origin git@github.com:me/store.git
vars git log                # run any git command in the store dir
vars sync                   # pull --rebase, then push
```

After a change, vars reminds you to `vars sync` when a remote is configured.

### On a new machine

Once your store has a remote, get it onto a new machine by **cloning the remote
into your local store**, not by creating a fresh store (which would diverge):

```sh
vars clone git@github.com:me/store.git   # clones into ~/.local/share/vars/store
vars get RPC_URL                         # ready, if that SSH key is loaded
```

`vars clone` sets `origin`, so `vars sync` works right away. If an empty local store exists,
clone replaces it (it refuses only if the local store holds secrets).
The key that authenticates the clone (your SSH key)
may differ from the key the store is encrypted with; clone tells you to `ssh-add`
the latter if it isn't loaded.

---

## How it works

Curious what's under the hood? Three ideas:

- **One file per secret.** Each value is an [age](https://age-encryption.org)-encrypted
  file under your store directory; scopes are just subdirectories.
- **Your SSH key is the lock.** The encryption key for each file is derived from
  a deterministic signature by your Ed25519/RSA key, via `ssh-agent`.
  (ECDSA and FIDO/`sk-` keys sign non-deterministically, so they can't be used.)
- **git is the history and the sync.** The store is an ordinary git repo, so
  `git log` is your history and `git push`/`pull` syncs encrypted secrets between your
  machines. git is optional: without it everything still works, versioning is opt-in.

The same SSH key must be available on every machine that opens the store: git
moves the encrypted files, and your SSH key is what decrypts them. On first run vars
picks the key in your agent (or asks, if there are several) and records its
fingerprint in `store.json`. To force a specific key (a non-standard path or disambiguation):

```sh
export VARS_SSH_KEY=~/.ssh/id_work
```

### Using a dedicated decryption key

You don't have to reuse your everyday login key. A common setup: a **separate key
just for vars**, kept alongside your others.

```sh
ssh-keygen -t ed25519 -f ~/.ssh/id_vars   # a key only for decrypting the store
```

- **Discovery is automatic.** vars scans `~/.ssh` and uses whichever key matches
  the store's fingerprint, `~/.ssh/id_vars` (or any name) is found on its own.
- **It won't interfere with logging into servers.** A non-default filename like
  `id_vars` is *not* offered by `ssh` or loaded by a bare `ssh-add`, so your login
  key stays the one used for servers.
- **Keep it passphrase-protected.** When you run a command that needs it in a
  terminal, vars runs `ssh-add ~/.ssh/id_vars` for you (prompting once) and proceeds;
  after that it's cached in the agent for the session.

Once `id_vars` is in the agent alongside your login key, it can be offered to
servers. To keep logins on your real key(s) only, set `IdentitiesOnly` globally and
**list each login key** (the `IdentityFile` lines accumulate into an allowlist;
anything not listed, including `id_vars`, is never offered):

```
# ~/.ssh/config
Host *
    IdentitiesOnly yes
    IdentityFile ~/.ssh/id_ed25519      # list every key you log in with
    IdentityFile ~/.ssh/id_rsa

Host github.com
    IdentitiesOnly yes
    IdentityFile ~/.ssh/id_github
```

---

## Security

- **Encryption:** age (ChaCha20-Poly1305). Each file's key is derived from a
  deterministic SSH signature (OpenSSH `SSHSIG`, namespace `vars.store.v1`) run
  through HKDF-SHA256.
- **No passphrase, no daemon, no socket**: signing goes through your
  `ssh-agent` or the key file.
- **What the repo reveals:** values are encrypted, but **key/scope names are
  not** (`prod/PRIVATE_KEY.age` is visible), and git history retains old
  encrypted values.
- **Only encrypted files are committed**
- **Break-glass:** every store ships a `README.md` showing how to unlock with
  `ssh-keygen` plus the decryption details, so you're never locked into the `vars` binary.
- **Permissions:** the store directory is `0700`, the access boundary.
- **Quantum:** the file cipher is symmetric (safe). The SSH keypair is the
  Shor-vulnerable link, as in every mainstream tool today; rotate long-lived
  secrets and don't treat the store as an eternal archive.

---

## Command reference

```sh
vars                          # first run: create the store
vars set <key> [value]        # add/update a key (prompts if omitted; "vars set KEY -" reads stdin)
vars get <key>                # print a value (KEY~N for N versions ago)
vars ls [scope]               # list keys as a tree (optional arg must be a scope)
vars scope ls                 # list scope prefixes
vars mv <old> <new>           # rename a key (-f to skip the prompt)
vars rm <key>...              # delete keys (-f to skip the prompt)
vars log <key>                # a key's change history (newest first)
vars import [scope] <file>    # import key=value pairs from a .env file
vars dump                     # print all keys and values (-f to skip the confirm)
vars init                     # scaffold .vars.yaml in the current directory
vars resolve [flags]          # resolve manifest keys as shell exports
vars git <args>               # run git in the store directory
vars sync                     # pull + push the store to its remote
vars clone <remote>           # clone the store from a remote repo
```

`resolve` flags: `-f/--file`, `-p/--profile`, `--dotenv`, `--fish`, `--partial`,
`--origin`. `set`/`import` take `--replace`/`--skip`; `mv`/`rm`/`dump` take `-f/--force`.

---

## Development

Requires Go 1.25+ and [just](https://github.com/casey/just). git is used at
runtime for versioning; `ssh-keygen` for tests.

```sh
just            # list all recipes
just check      # vet + lint + test
just test-all   # unit + integration tests
just smoke      # end-to-end smoke test against a temp store
just build      # build the binary
```

---

## Migrating from an older vars (v0.5)

The store format changed, so migrate with the old binary's `dump` piped into the
new `import` (no plaintext touches disk):

```sh
vars-new import <(vars-0.5 dump --dotenv)
```

History does not carry over (only current values). The old store is left
untouched at its old location.
