# netsec

`netsec` is an embeddable C++20 pre-load policy gate for untrusted ELF64
little-endian x86-64 `ET_DYN` images. It treats the input strictly as bytes:
it does not `mmap`, `dlopen`, relocate, or run an initializer.

The checker parses program headers, `PT_DYNAMIC`, dynamic string/symbol/hash
tables, and RELA relocations. Every file range and size calculation is checked
for overflow and for containment in a single `PT_LOAD`. Findings carry the
exact offending file offset (a program header, dynamic entry, RELA record, or
table byte range); relocation findings also carry the target's file offset
when the destination is file-backed.

## Policy

The gate rejects an image when any of these conditions is observed:

- A `JUMP_SLOT` exists without `DT_BIND_NOW`, `DF_BIND_NOW`, or `DF_1_NOW`.
- A `GLOB_DAT`, `JUMP_SLOT`, `COPY`, IRELATIVE, or function/code-pointer
  relocation writes a destination that is still writable after the
  page-rounded `PT_GNU_RELRO` range is applied.
- A relocation target is outside a single writable `PT_LOAD`, overlaps code,
  overlaps dynamic metadata, or crosses the effective RELRO boundary.
- `PT_LOAD` permissions are RWX, LOAD ranges overlap, or different LOAD
  permissions share a mapped page.
- A constructor, pre-constructor, or finalizer array is not fully covered by
  the effective RELRO pages.
- A table is truncated, not contained in one LOAD, malformed, or would
  overflow an integer calculation.
- RELRO itself is empty, outside a writable LOAD, or its rounded pages extend
  outside the containing LOAD.

An unaligned `PT_GNU_RELRO` is not rejected by itself. The policy computes
`[floor(start, page), ceil(end, page))`, which is the range the loader can
actually make read-only. `DT_RELR` and Android packed relocations are rejected
because their effects cannot be proven with the required RELA model.

## API

```cpp
#include "netsec.hpp"

std::vector<std::uint8_t> image = read_untrusted_file();
netsec::PolicyResult report = netsec::analyze(image);

if (!report.allowed) {
    for (const netsec::Finding& finding : report.findings) {
        // finding.code, finding.message, finding.source.file_offset
        // finding.relocation / finding.target when applicable
    }
}

report.allowed(); // Equivalent to report.allowed.value.
```

The policy result also exposes `decision`, `summary` (LOAD permissions,
relocation counts, BIND_NOW state, declared and page-rounded RELRO ranges), and
`format()` for a deterministic text report. The policy page size defaults to
4096 and can be changed through `PolicyOptions`.

## CLI

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=RelWithDebInfo
cmake --build build
ctest --test-dir build --output-on-failure

./build/netsec-check untrusted.so
./build/netsec-check --json --page-size 16384 untrusted.so
```

`netsec-check` exits `0` for an allow decision, `1` for a reject decision, and
`2` for invalid usage.

## Tests

`tests/netsec_tests.cpp` builds bounded ELF64 fixtures and distinguishes:

- full RELRO (including `DT_BIND_NOW`, `DF_BIND_NOW`, and `DF_1_NOW`)
- `-z relro` with lazy binding
- unaligned RELRO whose final range is page-rounded
- a legal plugin without PLT/JUMP_SLOT relocations
- truncation of string, symbol, hash, and RELA tables
- overlapping LOADs, RWX mappings, text relocations, and RELRO overflow
- writable GOT, GLOB_DAT, COPY/function-pointer, and constructor-array windows
- SysV and GNU hash symbol-table extent validation
