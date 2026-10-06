# livecheck

`livecheck` is a small Go library that checks a labeled state graph for
the liveness property **every request is eventually answered by a
response carrying the same id**. Runs may be infinite, so the checker
reasons over the product of the graph with an obligation state (the set
of request ids currently owed a response) rather than over finite
traces only.

## Model

- A `Graph` is a set of states with edges labeled by `Action`s.
- `Action{Kind: Request, ID: id}` raises an obligation for `id`;
  `Action{Kind: Response, ID: id}` discharges it; `Kind: Other` is a
  neutral label.
- Multiple concurrent requests are tracked **separately by their
  explicit ids** in the obligation set.

## Violations

`Check` reports a `*Counterexample` (nil means the property holds):

- **Lasso**: a finite `Prefix` plus a repeatable `Cycle`. Along the
  whole cycle `PendingID` is owed a response and is never answered, so
  repeating the cycle forever yields an infinite run that owes a
  response forever.
- **Terminal**: `Terminal` is true and the cycle is empty — the prefix
  ends in a state with no outgoing edges that still owes `PendingID` a
  response. Ending in a terminating state while owing a response counts
  as a failure.

Every counterexample returned by `Check` has already been confirmed by
`Verify`, an independent replay that re-simulates prefix and cycle from
the graph alone — re-checking every edge, recomputing the obligation
set, and confirming the cycle returns to its start state, reproduces
the pending set, and never answers the owed id. `Verify` is exported so
counterexamples can be cross-checked by a third party.

## Example

```go
g := livecheck.NewGraph("idle")
g.AddEdge("idle", "waiting", livecheck.Action{Kind: livecheck.Request, ID: "job-1"})
g.AddEdge("waiting", "waiting", livecheck.Action{Kind: livecheck.Other, ID: "tick"})
g.AddEdge("waiting", "idle", livecheck.Action{Kind: livecheck.Response, ID: "job-1"})

ce, err := livecheck.Check(g)
if err != nil {
	panic(err)
}
if ce != nil {
	fmt.Print(ce) // lasso: the "tick" self-loop never answers job-1
}
```

## Development

```
go test ./...
```
