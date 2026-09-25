#!/usr/bin/env sh
# GoReleaser invokes this as its `gobinary` (see .goreleaser.yaml). GoReleaser
# calls it as `garble.sh build <flags…>`, so we inject garble's own flags
# *before* the `build` verb it appends and let garble forward the rest to
# `go build` unchanged.
#
# Max obfuscation (Decision 18):
#   -literals    encrypt string / numeric constants (decrypted at runtime)
#   -tiny        strip the symbol table + file:line metadata → no readable
#                stack traces, smallest binary, hardest to decompile
#   -seed=random per-build random seed so identifier hashing is not
#                reproducible or reversible across releases
#
# Escape hatch: PIE_NO_OBFUSCATE=1 falls through to a plain `go` invocation so
# `make snapshot` and quick local builds stay fast (garble builds are slow).
set -eu

if [ "${PIE_NO_OBFUSCATE:-0}" = "1" ]; then
	exec go "$@"
fi

if ! command -v garble >/dev/null 2>&1; then
	echo "garble not found. Install it with:" >&2
	echo "    go install mvdan.cc/garble@latest" >&2
	echo "or set PIE_NO_OBFUSCATE=1 to build without obfuscation." >&2
	exit 1
fi

exec garble -literals -tiny -seed=random "$@"
