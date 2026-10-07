# filewrap

`filewrap` wraps a positioned-read backend (`io.ReaderAt` + `io.Closer`)
with a shared sequential cursor. The zero-dependency `File` is safe for
concurrent use.

## Semantics

- **Shared cursor reads** — `Read` consumes a cursor shared by all
  goroutines; concurrent reads are serialized so each sees a distinct,
  monotonically advancing offset.
- **Positioned reads** — `ReadAt` reads at an absolute offset and never
  observes or mutates the cursor; positioned reads run concurrently.
- **Short reads** — the cursor advances only by the number of bytes
  actually returned.
- **Close vs. read** — arbitrated by entry point: a read that has
  already entered runs to completion even while `Close` is pending;
  operations arriving after `Close` fail with `ErrClosed`.
- **Repeated close** — only the first `Close` closes the backend; later
  calls return `ErrClosed`.

## Test

```sh
go test -race ./...
```

Tests use an in-memory virtual backend plus channel barriers to verify
concurrent positioned reads, EOF behavior, close/read interleaving, and
exactly-once close.
