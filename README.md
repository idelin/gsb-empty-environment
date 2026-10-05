# barrier

A reusable phased (cyclic) barrier for Go.

Participants synchronize across a sequence of numbered rounds. Each round
settles exactly once — either because the last expected participant arrived
(`Released`) or because `Cancel` was called (`Cancelled`) — and every waiter
of that round is woken with a `Result{Round, Reason}`.

## Semantics

- `New(parties)` creates a barrier expecting `parties` arrivals per round.
- `Arrive()` blocks until the current round settles and returns its `Result`.
- `ArriveAndLeave()` arrives and declares departure: from the next round the
  caller is no longer counted among the expected participants.
- `Join()` registers a new participant; it takes effect only from the next
  round, so `Join` blocks until the current round settles and returns the
  round number in which the caller may first arrive.
- `Cancel()` settles the current round as `Cancelled`, waking all of its
  waiters, and advances the barrier to the next round.
- `Round()`, `Parties()` and `Arrived()` expose the current state.

## Consistency guarantee

Membership deltas (joins and departures) are applied exactly once, at the
single point where a round settles. Because settlement is serialized under
one mutex and rotates the round's generation, a last arrival, a departure
declaration and a cancellation that coincide on the same round can never
double-decrement the participant count. This is verified by
`TestLastArrivalAndCancelRaceSameRound` and `TestConcurrentArrivalLeaveCancel`
under `go test -race`.

## Test

```sh
go test -race ./...
```
