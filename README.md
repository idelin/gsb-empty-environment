# doublebuf

A Go double-buffered store with versioned snapshots.

## Semantics

- **Two buffers, one active.** Readers `Acquire` the active buffer; a
  writer prepares the standby buffer via `Begin` and publishes it with
  `Commit`, which atomically swaps the slots and bumps the version.
- **Failed writes keep the old version.** Staged content is invisible
  until `Commit`; `Abort` (or simply never committing) leaves the active
  buffer untouched.
- **Pinned buffers are never reused.** A retired buffer stays readable by
  the readers that pinned it until they `Release`. With only two buffers,
  a third update blocks in `Begin` until the last reader of the retired
  buffer releases it, or the caller's context is canceled.
- **Versions are monotonic.** The initial content is version 1; every
  successful `Commit` increments it. Committing or aborting an already
  finished update returns `ErrUpdateClosed`.

## Example

```go
s := doublebuf.New([]byte("v1-content"))

view := s.Acquire()          // pins version 1
defer view.Release()
view.Version(); view.Bytes()

u, err := s.Begin(ctx)       // waits if the standby buffer is pinned
if err != nil { /* ctx canceled */ }
u.Write([]byte("v2-content"))
version, err := u.Commit()   // publishes version 2; old readers keep v1
```

## Test

```sh
go test -race ./...
```
