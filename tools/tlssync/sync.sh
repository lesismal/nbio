#!/usr/bin/env bash
# sync.sh — re-import Go's crypto/tls into crypto/tls/ of this repo.
#
# Usage: tools/tlssync/sync.sh <go-tag> [<go-git-dir>]
#   <go-tag>      Go release tag, e.g. go1.27.3
#   <go-git-dir>  a clone of https://go.googlesource.com/go (default: .src/go).
#                 A --filter=blob:none clone is enough.
#
# What it does (and nothing else):
#   1. Exports src/crypto/tls and the two small key-schedule packages
#      (crypto/internal/fips140/tls12, tls13) at <go-tag>.
#   2. Replaces crypto/tls/ wholesale, keeping only our own files
#      (listed in OWN_FILES below and everything under internal/ that is a shim).
#   3. Rewrites imports of Go-internal packages to the shims (mechanical sed).
#   4. Applies tools/tlssync/patches/*.patch in order (the hand-made diff).
#   5. Records the tag in crypto/tls/UPSTREAM.
#
# Review the result with `git diff`, then run tools/tlssync/check.sh.
set -euo pipefail

TAG=${1:?usage: sync.sh <go-tag> [<go-git-dir>]}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GODIR=${2:-$ROOT/.src/go}
MOD=github.com/lesismal/nbio/crypto/tls
DST=$ROOT/crypto/tls

# Our own files inside crypto/tls/. Everything else there comes from Go.
OWN_FILES=(nonblock.go nonblock_test.go arco_regress_test.go arco_fuzz_test.go arco_assumptions_test.go arco_review3_test.go LICENSE README.md UPSTREAM)

git -C "$GODIR" rev-parse -q --verify "refs/tags/$TAG" >/dev/null ||
	git -C "$GODIR" fetch -q origin "refs/tags/$TAG:refs/tags/$TAG"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git -C "$GODIR" archive "$TAG" src/crypto/tls src/crypto/internal/fips140/tls12 \
	src/crypto/internal/fips140/tls13 | tar -x -C "$TMP"
SRC=$TMP/src/crypto

# 1. Save our own files and shims, wipe, re-import.
mkdir -p "$TMP/own"
for f in "${OWN_FILES[@]}"; do
	[ -e "$DST/$f" ] && cp "$DST/$f" "$TMP/own/"
done
[ -d "$DST/internal" ] && cp -R "$DST/internal" "$TMP/own/internal"
[ -d "$DST/testdata/fuzz" ] && cp -R "$DST/testdata/fuzz" "$TMP/own/fuzz" # our fuzz corpus
rm -rf "$DST"
mkdir -p "$DST"
cp -R "$SRC/tls/." "$DST/"
# Not imported: the fipsonly package and the BoringCrypto-only defaults.
rm -rf "$DST/fipsonly" "$DST/defaults_boring.go"
rm -rf "$DST/internal"
[ -d "$TMP/own/internal" ] && cp -R "$TMP/own/internal" "$DST/internal"
for f in "${OWN_FILES[@]}"; do
	[ -e "$TMP/own/$f" ] && cp "$TMP/own/$f" "$DST/"
done
[ -d "$TMP/own/fuzz" ] && cp -R "$TMP/own/fuzz" "$DST/testdata/fuzz"

# The key schedule packages are copied verbatim next to their shims.
for p in tls12 tls13; do
	mkdir -p "$DST/internal/fips140/$p"
	find "$DST/internal/fips140/$p" -name '*.go' ! -name 'shim*.go' -delete
	cp "$SRC/internal/fips140/$p/$p.go" "$DST/internal/fips140/$p/"
done
cp -R "$SRC/tls/internal/fips140tls/." "$DST/internal/fips140tls/"

# 2. Mechanical import rewrite. Each Go-internal import maps to a shim with
# the same package name and the same exported API.
REWRITE=(
	's#"crypto/tls/internal/fips140tls"#"'$MOD'/internal/fips140tls"#'
	's#"crypto/internal/fips140/tls12"#"'$MOD'/internal/fips140/tls12"#'
	's#"crypto/internal/fips140/tls13"#"'$MOD'/internal/fips140/tls13"#'
	's#"crypto/internal/fips140/aes/gcm"#"'$MOD'/internal/fips140/aes/gcm"#'
	# cipher_suites.go asserts crypto/aes's block to the internal *aes.Block;
	# the gcm shim takes the public cipher.Block instead.
	'/fipsaes "crypto\/internal\/fips140\/aes"/d'
	's#gcm\.NewGCMForTLS1\([23]\)(aes\.(\*fipsaes\.Block))#gcm.NewGCMForTLS1\1(aes)#'
	's#"crypto/internal/fips140/hkdf"#"'$MOD'/internal/fips140/hkdf"#'
	's#"crypto/internal/fips140/hmac"#"'$MOD'/internal/fips140/hmac"#'
	's#"crypto/internal/fips140/sha256"#"'$MOD'/internal/fips140/sha256"#'
	's#"crypto/internal/fips140/sha512"#"'$MOD'/internal/fips140/sha512"#'
	's#"crypto/internal/fips140deps/byteorder"#"'$MOD'/internal/byteorder"#'
	's#"crypto/internal/fips140"$#"'$MOD'/internal/fips140"#'
	's#"crypto/internal/boring"#"'$MOD'/internal/boring"#'
	's#"crypto/internal/cryptotest"#"'$MOD'/internal/cryptotest"#'
	's#"crypto/internal/rand"#"'$MOD'/internal/rand"#'
	's#"internal/byteorder"#"'$MOD'/internal/byteorder"#'
	's#"internal/cpu"#"'$MOD'/internal/cpu"#'
	's#"internal/godebug"#"'$MOD'/internal/godebug"#'
	's#"internal/testenv"#"'$MOD'/internal/testenv"#'
)
SED_ARGS=()
for r in "${REWRITE[@]}"; do SED_ARGS+=(-e "$r"); done
find "$DST" -name '*.go' -print0 | while IFS= read -r -d '' f; do
	case "$f" in "$DST/internal/"*) case "$f" in */tls12/tls12.go | */tls13/tls13.go | */fips140tls/*) ;; *) continue ;; esac ;; esac
	sed -i.bak "${SED_ARGS[@]}" "$f" && rm -f "$f.bak"
done

# 3. Hand-made patches (kept as small as possible; each one is documented).
for p in "$ROOT"/tools/tlssync/patches/*.patch; do
	[ -e "$p" ] || continue
	echo "applying $(basename "$p")"
	git -C "$ROOT" apply --whitespace=nowarn "$p"
done

echo "$TAG" >"$DST/UPSTREAM"
gofmt -l "$DST" || true
echo "imported crypto/tls from $TAG"
