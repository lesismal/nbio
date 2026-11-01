#!/usr/bin/env bash
# latest.sh — fail if Go has released a newer patch of the minor version our
# crypto/tls copy comes from (crypto/tls/UPSTREAM). Any Go point release may
# carry crypto/tls security fixes that vulndb lists only later.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
UP=$(cat "$ROOT/crypto/tls/UPSTREAM")
MINOR=$(echo "$UP" | sed -E 's/^(go[0-9]+\.[0-9]+).*/\1/')
LATEST=$(curl -fsSL 'https://go.dev/dl/?mode=json' |
	python3 -c 'import json,sys; m=sys.argv[1]; print(next((r["version"] for r in json.load(sys.stdin) if r["version"]==m or r["version"].startswith(m+".")), ""))' "$MINOR")
NEWEST=$(curl -fsSL 'https://go.dev/dl/?mode=json' | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["version"])')
echo "copy: $UP  latest $MINOR.x: ${LATEST:-unknown}  newest Go: $NEWEST"
if [ -n "$LATEST" ] && [ "$LATEST" != "$UP" ]; then
	echo "Go $LATEST is out: run tools/tlssync/sync.sh $LATEST (crypto/tls/README.md)."
	exit 1
fi
if [ "$MINOR" != "$(echo "$NEWEST" | sed -E 's/^(go[0-9]+\.[0-9]+).*/\1/')" ]; then
	echo "note: a newer Go minor ($NEWEST) exists; plan the minor port (crypto/tls/README.md)."
fi
