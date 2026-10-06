# transducer

Bounded equivalence checking for finite transducers: Mealy-style machines
whose transitions emit possibly-**empty** output strings.

## Equivalence semantics

Given a public finite input-length bound `L` (`Config.MaxLen`), two
machines are equivalent iff every *complete* input string of length
exactly `L` makes both machines emit the same concatenated output.

- Per-edge output differences do **not** imply inequivalence: a machine
  may defer output via empty edge outputs and catch up later in the same
  input string. Only the totals at the end of a complete run are compared.
- A missing transition is an *error output*: the machine emits a
  distinguished error marker and halts. Runs compare by the pair
  `(output, hitMissingTransition)`.
- Equivalence is decided over outputs, not over final/accepting states.

## Algorithm

BFS over the reachable product graph. Each node carries both machine
states plus the *lag* — the pending output difference after stripping the
common prefix (at most one side is non-empty). A node is a counterexample
when the lag can never reconcile (e.g. both sides have pending output, or
one machine already halted while owing output); a node at depth `L` is a
counterexample when the totals differ. Every reported witness is
re-verified by full execution (`Machine.Run`) on both machines before
being returned.

## Budget and resumption

`Config.Budget` bounds the number of product nodes expanded (`<= 0` means
unlimited). When the budget is exhausted, `Compare`/`Resume` return
`Unknown` with a `Frontier` — an exported, serializable snapshot of the
unexplored queue. Pass it to `Resume` (with a fresh budget) to continue
where the search left off.

```go
v, _ := transducer.Compare(a, b, transducer.Config{
    Alphabet: []rune("ab"), MaxLen: 8, Budget: 1000,
})
for v.Status == transducer.Unknown {
    v, _ = transducer.Resume(a, b, cfgWithFreshBudget, v.Frontier)
}
```

## Testing

`go test ./...` cross-validates the checker against brute-force
enumeration of all bounded input strings on randomly generated machines
(empty outputs, missing transitions included), and checks that
budget-limited runs resumed to completion agree with one-shot runs.
