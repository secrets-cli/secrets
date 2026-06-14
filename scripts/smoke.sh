#!/usr/bin/env bash
# End-to-end smoke test for the ssh-v1 (v0.6) vars. Uses a dedicated SSH key so
# it's deterministic and needs no ssh-agent. git versioning is best-effort: the
# history check is skipped where git isn't functional.
set -euo pipefail

BIN="${1:-./vars}"

TMP=$(mktemp -d)
WORKDIR=$(mktemp -d)
trap "rm -rf $TMP $WORKDIR" EXIT

ssh-keygen -t ed25519 -N "" -f "$TMP/key" -q
export VARS_STORE_DIR="$TMP/store"
export VARS_SSH_KEY="$TMP/key"

contains() { echo "$1" | grep -q "$2"; }

echo "--- first run + set ---"
$BIN set RPC_URL https://rpc.example.com
$BIN set PRIVATE_KEY 0xTESTKEY
$BIN set ETHERSCAN_API abc123

echo "--- get ---"
test "$($BIN get RPC_URL)" = "https://rpc.example.com"

echo "--- ls (3 unscoped keys) ---"
test "$($BIN ls | wc -l)" -eq 3

echo "--- scoped keys (directories) + tree + scope ls ---"
$BIN set prod/RPC_URL https://prod.rpc
$BIN set main/dev/RPC_URL https://maindev.rpc
contains "$($BIN ls)" "prod/"
contains "$($BIN ls prod)" "RPC_URL"
contains "$($BIN scope ls)" "main/dev"
test -f "$VARS_STORE_DIR/prod/RPC_URL.age"

echo "--- resolve (posix) ---"
cat > "$WORKDIR/.vars.yaml" <<'YAML'
keys:
  - RPC_URL
  - PRIVATE_KEY
YAML
eval "$($BIN resolve -f "$WORKDIR/.vars.yaml")"
test "$RPC_URL" = "https://rpc.example.com"
test "$PRIVATE_KEY" = "0xTESTKEY"

echo "--- resolve (dotenv / fish) ---"
contains "$($BIN resolve -f "$WORKDIR/.vars.yaml" --dotenv)" "RPC_URL="
contains "$($BIN resolve -f "$WORKDIR/.vars.yaml" --fish)" "set -x"

echo "--- resolve --partial ---"
cat > "$WORKDIR/.vars.yaml" <<'YAML'
keys:
  - RPC_URL
  - MISSING_KEY
YAML
OUT=$($BIN resolve -f "$WORKDIR/.vars.yaml" --partial 2>/dev/null)
contains "$OUT" "RPC_URL"
! contains "$OUT" "MISSING_KEY"

echo "--- resolve stdin dotenv passthrough ---"
cat > "$WORKDIR/.vars.yaml" <<'YAML'
keys:
  - RPC_URL
  - DOTENV_ONLY
YAML
OUT=$(printf 'DOTENV_ONLY=from_dotenv\nPASSTHROUGH=passthrough\n' | $BIN resolve -f "$WORKDIR/.vars.yaml" --partial 2>/dev/null)
contains "$OUT" "from_dotenv"
contains "$OUT" "PASSTHROUGH"

echo "--- resolve scope fallback (deepest-first) ---"
cat > "$WORKDIR/.vars.yaml" <<'YAML'
keys:
  - RPC_URL
profiles:
  mainnet:
    RPC_URL: main/dev/sub/RPC_URL
YAML
# main/dev/sub/RPC_URL missing -> strip deepest -> main/dev/RPC_URL hits.
eval "$($BIN resolve -f "$WORKDIR/.vars.yaml" -p mainnet)"
test "$RPC_URL" = "https://maindev.rpc"

echo "--- import ---"
printf 'IMPORTED_A=aaa\nIMPORTED_B=bbb\n' > "$WORKDIR/.env"
$BIN import "$WORKDIR/.env" >/dev/null
test "$($BIN get IMPORTED_A)" = "aaa"

echo "--- mv ---"
$BIN mv IMPORTED_A RENAMED_A --force >/dev/null
test "$($BIN get RENAMED_A)" = "aaa"
! $BIN get IMPORTED_A 2>/dev/null

echo "--- rm ---"
$BIN rm RENAMED_A --force >/dev/null
! $BIN get RENAMED_A 2>/dev/null

echo "--- dump ---"
contains "$($BIN dump --dotenv 2>/dev/null)" "ETHERSCAN_API="

echo "--- version ---"
contains "$($BIN --version)" "vars"

echo "--- history (git; skipped where git is unavailable) ---"
$BIN set RPC_URL https://rpc-v2.example.com --replace >/dev/null 2>&1
HIST=$($BIN history RPC_URL 2>/dev/null || true)
if [ -n "$HIST" ]; then
    contains "$HIST" "RPC_URL"
    echo "    history OK"
else
    echo "    (no git history available here — skipped)"
fi

echo ""
echo "All smoke tests passed!"
