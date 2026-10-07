#!/usr/bin/env bash
# Builds the server and the phone simulator, pairs them by code, and runs both
# SDK demos against them. Uses the default ports 8778-8780 on loopback.
set -euo pipefail
[ "${DEBUG:-}" = 1 ] && set -x
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
cleanup() { kill $(jobs -p) 2>/dev/null || true; wait 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

cd "$ROOT"
go build -o "$WORK/bin/" ./server/cmd/droidline ./server/cmd/droidline-fakephone
DL="$WORK/bin/droidline"
cat > "$WORK/config.toml" <<CFG
name = "ci"
lang = "en"
[agent]
listen = "127.0.0.1:8779"
[client]
listen = "127.0.0.1:8780"
[discovery]
listen = "127.0.0.1:8778"
CFG

"$DL" --home "$WORK" serve > "$WORK/serve.log" 2>&1 &
sleep 1
(cd "$WORK" && exec "$WORK/bin/droidline-fakephone") > "$WORK/phone.log" 2>&1 &
for _ in $(seq 50); do grep -q "droidline pair [0-9]" "$WORK/phone.log" && break; sleep 0.2; done
CODE=$(grep -o 'droidline pair [0-9]\{6\}' "$WORK/phone.log" | head -1 | awk '{print $3}' || true)
if [ -z "$CODE" ]; then echo "--- serve.log"; cat "$WORK/serve.log"; echo "--- phone.log"; cat "$WORK/phone.log"; exit 1; fi
"$DL" pair "$CODE" --name ci-phone
"$DL" devices

cd "$ROOT/examples"
PYTHONPATH="$ROOT/sdk/python/src" python demo.py
npm install --silent --no-audit --no-fund
node demo.mjs
rm -f screen.json screen.png
