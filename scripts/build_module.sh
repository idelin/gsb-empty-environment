#!/usr/bin/env bash
# Builds the importable C++20 module for Apple Clang, whose Makefile-based
# CMake generator does not yet drive module dependency scanning.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/build/module}"
mkdir -p "$OUT"

CXX="${CXX:-clang++}"
COMMON=(-std=c++20 -Wall -Wextra -fmodules -fcxx-modules -I"$ROOT/include")

"$CXX" "${COMMON[@]}" \
    -x c++-header --precompile -fmodule-header \
    "$ROOT/include/gsb/reassembly.hpp" \
    -o "$OUT/reassembly-hdr.pcm"

"$CXX" "${COMMON[@]}" \
    -fmodule-file="$OUT/reassembly-hdr.pcm" \
    --precompile -x c++-module \
    "$ROOT/src/gsb/reassembly.cppm" \
    -o "$OUT/gsb-reassembly.pcm"

"$CXX" "${COMMON[@]}" \
    -fmodule-file=gsb.reassembly="$OUT/gsb-reassembly.pcm" \
    -c "$OUT/gsb-reassembly.pcm" -o "$OUT/gsb-reassembly.o"

"$CXX" "${COMMON[@]}" \
    -fmodule-file=gsb.reassembly="$OUT/gsb-reassembly.pcm" \
    "$ROOT/tests/module_smoke.cpp" \
    "$OUT/gsb-reassembly.o" \
    -o "$OUT/module_smoke"

"$OUT/module_smoke"
echo "module build and import smoke test passed ($OUT)"
