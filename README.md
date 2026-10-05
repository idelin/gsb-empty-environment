# spcopy — sparse file copy tool (C++17)

Copies sparse files preserving both logical content and hole layout, with
snapshot consistency checks and resumable copies.

## Layout

- `src/posix_adapter.h/.cpp` — injectable POSIX adapter layer. All syscalls
  go through `PosixAdapter`; short reads/writes are normal (the engine loops
  around them). `RealPosixAdapter` wraps real syscalls and degrades to
  zero-block scanning when `SEEK_DATA`/`SEEK_HOLE` is unavailable.
- `src/sparse_copy.h/.cpp` — copy engine, recovery cursor, `verify_copy`.
- `tests/fake_adapter.h` — in-memory adapter with fault injection (short
  reads/writes, EIO after N bytes, mid-copy mutation hooks).
- `tests/test_main.cpp` — test suite. Run with `make test`.

## Guarantees

- **Snapshot source**: source identity (dev, ino, length, mtime, ctime) is
  captured before the copy and re-checked after; any drift, an unexpected
  EOF, or a mid-copy truncation fails with `SourceChangedError`.
- **Sparse fidelity**: data extents are located via `SEEK_DATA`/`SEEK_HOLE`
  (or zero-block scan fallback); holes are skipped, never written.
- **Recovery cursor**: `<dst>.spcopy` records source dev/ino/length plus the
  resume offset, persisted before each chunk is written. A cursor bound to a
  different source identity or length is rejected (`CursorMismatchError`).
- **Dirty tail**: fresh copies truncate the destination first; every copy
  finishes with `ftruncate(dst, src_len)`, removing stale trailing data.
- **Verification**: `verify_copy` compares length, logical bytes, and the
  data/hole extent map — never physical block occupancy.
