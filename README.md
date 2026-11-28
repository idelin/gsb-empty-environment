# reghive_audit

Embeddable, zero-dependency C++17 raw-byte security auditor for offline
Windows Registry Hive images (`regf` base block + `hbin` blocks +
`nk`/`vk`/`lf`/`lh`/`li`/`ri`/`db` cells).

The library never trusts a field, never reads outside the supplied buffer,
bounds every pointer dereference, and reports a precise `AuditStatus` for
the first structural violation. It is built for binary-safe forensic
hosts that must survive deliberately malformed or hostile hive files.

## Layout

- `include/reghive_audit.hpp` — public API, status codes, options and report.
- `src/reghive_audit.cpp` — three-phase auditor implementation (no STL
  containers beyond `vector`/`unordered_*`; no external dependencies).
- `tests/hive_builder.hpp` — dependency-free hand builder for micro hives.
- `tests/test_reghive_audit.cpp` — valid micro hive plus attack samples.
- `Makefile` / `CMakeLists.txt` — build and test entry points.

## Public API

```cpp
#include "reghive_audit.hpp"
using namespace reghive_audit;

AuditOptions opt;
opt.max_tree_depth = 512;                 // bound C++ recursion
AuditReport r = audit(buffer, size, opt); // never throws
if (!r.ok) {
    // r.status, r.message, r.file_offset, r.hive_offset describe the fault.
}

uint32_t xsum = regf_checksum(first_508_bytes, 508);
```

## What is audited

1. **`regf` base block (first 4096 bytes).**
   - `regf` magic.
   - `PrimarySequence == SecondarySequence`.
   - `HiveBinsDataSize + 4096 == file length` using 64-bit arithmetic, so a
     32-bit wrap, a truncated body or trailing hidden bytes are rejected.
   - Exact checksum: XOR of the 127 little-endian 32-bit words over the
     first 508 bytes, compared against the stored word at `0x1FC`.
   - `RootCellOffset` presence (alignment/bounds resolved during traversal).

2. **Sequential `hbin` / cell scan (offset 4096 onward).**
   - `hbin` magic, 4096-aligned continuous relative offsets, sane block size.
   - Each cell size is non-zero, a multiple of 8, at least 8 bytes and fully
     inside its block; cells tile each `hbin` exactly with no overrun.
   - Negative sizes mark allocated cells, positive sizes free cells. The
     magnitude is computed by clearing the high bit (no signed overflow,
     including the `INT32_MIN` pattern).

3. **Key-tree traversal from `RootCellOffset`.**
   - Recursive walk of `nk` nodes, `lf`/`lh`/`li`/`ri` indexes and value
     lists, with a hard maximum depth that also covers nested `ri`/`li`.
   - A visited-`nk` offset set blocks directed cycles and illegal sharing of
     one `nk` cell across multiple parents.
   - Every reference must be 8-byte aligned, in range, a cell boundary, and
     allocated; a pointer landing inside another cell is reported as
     `CellOverlap`, a free target as `DanglingReference`.
   - `vk` inline data honors the high-bit flag and 4-byte cap; external data
     and `db` big-data segments must resolve to allocated cells whose
     payloads cover the declared length, and must never overlap metadata
     cells (`nk`/`vk`/lists/`db` control structures).

## Build and test

```sh
make test           # clang++, -Wall -Wextra -Wconversion -Wsign-conversion -Werror
```

The same strict flags are wired into the CMake library and test targets.

## Test coverage

The suite hand-builds a valid micro hive exercising every cell type and
includes dedicated attack samples for checksum tampering, sequence
desync, bad magic, trailing/truncated bytes, cell overlap, bad `hbin`
headers, an `nk` subkey directed cycle, an out-of-bounds `vk` offset,
unaligned references, zero-length cells, a `db` segment overrun and
tree-depth exhaustion, plus the `0x00000000` / `0xFFFFFFFF` checksum
boundary vectors.
