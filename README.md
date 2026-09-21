# resourcepool

A bounded pool for resources that are slow to create and may fail to create.
Standard library only (`context`, `sync`, `errors`).

## Guarantees

- The pool bounds **creating + in-use** resources at `max`; a slow creation
  holds a slot until it resolves.
- If `Acquire` is cancelled while its creation is still in flight, the
  late-arriving resource is **reclaimed** (destroyed), never leaked:
  cancelling 100 times leaves 0 background resources.
- `Release(bad=true)` **destroys** the resource instead of reusing it;
  releasing the same lease twice returns `ErrDoubleReturn`.
- `Close(ctx, WaitForReturn)` blocks until borrowed leases come back;
  `Close(ctx, Force)` returns immediately and destroys stragglers on return.
- Factory, timing, and failures are injected by the caller/tests.

## Usage

```go
p := pool.New(8, factory, destroy) // factory: func(ctx) (any, error)

l, err := p.Acquire(ctx)           // respects ctx while waiting/creating
res := l.Value()
err = l.Release(false)             // true => bad, destroy instead of reuse

p.Close(ctx, pool.WaitForReturn)   // or pool.Force
```

## Verify

```sh
go test -race -count=1 -v ./...   # tests incl. Example
go run ./cmd/demo                 # runnable walkthrough
```
