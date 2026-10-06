# reqresp

A small Go library that checks a liveness property of action-labeled state
graphs: **every request is eventually answered by a response carrying the
same explicit id**. Infinite runs are allowed — the graph may loop forever
as long as no request is left unanswered along the way.

## Model

A `Graph` is a set of `Edge`s between named states. Each edge carries an
`Action`:

- `Request{ID}` — raises an obligation for `ID`;
- `Response{ID}` — discharges the obligation for `ID`;
- `Internal` — neither raises nor discharges.

Concurrent requests are tracked **separately by explicit id**: the monitor
keeps a set of pending ids. Re-requesting an id that is still pending, or
responding to an id that is not pending, is reported as a model error.

## Checking

`Check` explores the **product of the graph with an obligation monitor**
(graph state × pending-id set) and reports a violation when either:

- a **terminal state** (no outgoing edges) is reached with responses still
  owed — a finite counterexample; or
- a reachable **cycle keeps some id pending at every point around the
  loop** — an infinite counterexample, reported as a *lasso*: a finite
  `Prefix` plus a repeatable `Cycle` on which the ids in `Owed` are never
  answered.

## Independent replay

`Verify` re-checks a claimed counterexample without trusting the checker:
it re-simulates the trace edge by edge against the graph, rebuilds the
pending sets from scratch, and confirms that the cycle truly returns to
its entry product state (so it can repeat forever) and that every claimed
owed id stays pending throughout — or, for finite counterexamples, that
the run ends in a terminal state still owing a response.

## Example

```go
g, _ := reqresp.NewGraph("idle", []reqresp.Edge{
    {From: "idle", To: "busy", Action: reqresp.Action{Kind: reqresp.Request, ID: "job-1"}},
    {From: "busy", To: "busy", Action: reqresp.Action{Kind: reqresp.Internal}},
    {From: "busy", To: "idle", Action: reqresp.Action{Kind: reqresp.Response, ID: "job-1"}},
})
res, _ := reqresp.Check(g)
if !res.OK() {
    fmt.Println(res.Violation)          // prefix + cycle, owed=[job-1]
    err := reqresp.Verify(g, res.Violation) // independent replay: err == nil
}
```

## Tests

```
go test ./...
```
