# sparse-copy

Sparse file copy tool (C++17). Copies a file preserving both its **logical
content** and its **hole layout** (`SEEK_DATA` / `SEEK_HOLE`), pins the
source to a snapshot so any mid-copy mutation fails the copy, and supports
resumable copies via a cursor bound to the source identity and length.

## Layout

- `include/sparsecopy/posix_adapter.hpp` — injectable POSIX facade
  (`PosixAdapter`) plus the real syscall implementation
  (`RealPosixAdapter`). All I/O goes through this interface, so tests can
  inject short reads/short writes and mid-copy mutations.
- `include/sparsecopy/sparse_copy.hpp`, `src/sparse_copy.cpp` — the copy
  engine (`SparseCopier`), `ResumeCursor`, `SourceChangedError`.
- `src/main.cpp` — CLI: `sparse-copy SRC DST [CURSOR_FILE]`.
- `tests/` — `FakePosixAdapter` (in-memory sparse files, short-I/O
  injection, op hook) and the test suite.

## Design

**Snapshot pinning.** The engine `fstat`s the source at entry and again
after the final `fsync`; if dev/ino/size/mtime/ctime differ, or a `pread`
hits EOF before the snapshotted length, it throws `SourceChangedError`
(CLI exit code 2). The destination must then be discarded and the copy
restarted from a fresh snapshot.

**Hole preservation.** Extents are walked with `SEEK_DATA`/`SEEK_HOLE`;
only data extents are copied, and a final `ftruncate(dst, src_size)`
materializes the trailing hole at the exact source length. On platforms
without `SEEK_DATA` (e.g. older macOS SDKs) the real adapter falls back to
treating the file as one data extent — logically correct, just not sparse.

**Short I/O.** `pread`/`pwrite` may legally complete short; the engine
loops over both (and retries `EINTR`), so injected short reads/writes need
no special casing.

**Dirty destination.** A fresh copy starts with `ftruncate(dst, 0)`, which
also discards a dirty tail left by a previous longer file; the final
`ftruncate(dst, src_size)` handles any remaining length mismatch.

**Resume cursor.** `ResumeCursor{dev, ino, size, next_offset}` serializes
to a single line (`sparsecopy/1 <dev> <ino> <size> <offset>`).
`next_offset` is always a hole boundary, i.e. every extent before it is
fully copied. On restart the cursor is honored only if `(dev, ino, size)`
still match the source; otherwise the destination is rebuilt from scratch.

## Build & test

```sh
make            # builds sparse-copy and sparse-copy-tests
make check      # runs the test suite
```

Tests verify **logical content byte-for-byte and hole-layout maps** (via
`SEEK_DATA`/`SEEK_HOLE` scans) — never just physical occupancy (`st_blocks`).
Covered scenarios:

- data extents straddling the copy-buffer boundary, with 7-byte short
  reads / 5-byte short writes injected
- all-hole file (stays sparse, reads back as zeros)
- source truncated mid-copy and source modified mid-copy → both must fail
- pre-existing destination with garbage inside source hole ranges and a
  dirty tail beyond source EOF
- cursor round-trip, stale-identity rejection (dev/ino/length), and
  crash-and-resume completing to an identical copy
- end-to-end on the real filesystem, comparing logical bytes

## CLI

```sh
sparse-copy SRC DST [CURSOR_FILE]
```

Exit codes: `0` success, `1` I/O error, `2` source changed during copy.
