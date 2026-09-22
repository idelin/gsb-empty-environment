# ordered

Concurrent task execution with strictly ordered delivery, written with
only the Go standard library.

`Map` consumes elements from an input channel, runs a task function on at
most `window` elements concurrently, and emits results on the returned
channel in exact input order:

- At most `window` results may be completed but undelivered, so a slow
  head element applies backpressure and later completions can never
  overtake it.
- Input is never prefetched beyond the window, so infinite inputs are
  safe.
- Two error policies: `FailFast` (abort on the first task error, cancel
  in-flight work, deliver the failing result last) and `OrderedFail`
  (deliver in order up to and including the first error, then reclaim the
  rest).

## Usage

```go
out := ordered.Map(ctx, in, 4, ordered.OrderedFail, func(ctx context.Context, v int) (int, error) {
    return v * v, nil
})
for r := range out {
    // r.Index, r.Value, r.Err — delivered in input order
}
```

## Run

```sh
go test -race -v ./...   # tests (task, timing and failures injected)
go run ./cmd/demo        # live demo: unordered completions, ordered delivery
```
