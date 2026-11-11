# handlepool

Ownership-tracked file-handle pool for a Go subprocess proxy.

## Guarantees

- **Tenant ownership.** Every registered handle is bound to a `TenantID`.
  `Borrow`, `Return` and `Transfer` reject foreign tenants with
  `ErrNotOwner`.
- **Generation-stamped tokens.** A `Token` pairs a slot index with a
  monotonically increasing generation. Releasing a handle reuses the slot but
  mints a new generation, so a late or replayed old token fails with
  `ErrInvalidToken` and can never observe or close the new occupant.
- **Atomic batch transfer.** `Transfer` validates the whole batch (known,
  unborrowed, correctly owned, no duplicates) before mutating anything. Any
  failure leaves all handles with their original owners.
- **Close exactly once.** `Release` is the only path that closes a handle.
  The close goes through the injectable `Closer`; on failure (including an
  injected fault) the handle stays open, owned, and occupies its slot, so the
  descriptor cannot be reused or double-closed.

## Injecting the close system call

```go
// Production default (also used when no option is supplied).
p := handlepool.New(64) // uses handlepool.SyscallCloser -> h.Close()

// Tests inject a fake closer and/or a fault hook.
p := handlepool.New(64, handlepool.WithCloser(handlepool.CloserFunc(func(h handlepool.Handle) error {
    return nil // record, fail, etc.
})))
p.SetFault(func(h handlepool.Handle) error {
    return handlepool.ErrFaultInjected // abort close before the syscall
})
```

`*os.File` already satisfies `handlepool.Handle` (`Fd`/`Close`).

## Typical flow

```go
tok, err := pool.Register(tenant, file)
h, err := pool.Borrow(tenant, tok)   // exclusive use
err = pool.Return(tenant, tok)       // give it back
err = pool.Transfer(src, dst, []handlepool.Token{tok}) // atomic handoff
err = pool.Release(tok)              // close exactly once, free the slot
```

Run the tests, including the data-race detector and fault/concurrency cases:

```sh
go test -race -count=1 ./...
```
