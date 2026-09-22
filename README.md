# rendezvous

Multi-party, round-based rendezvous (cyclic barrier) for Go, standard
library only (`sync.Mutex` + `sync.Cond`).

- **Rounds**: a round releases only when every expected participant has
  arrived; `Arrive` returns exactly at round boundaries.
- **Dynamic membership**: `Leave` shrinks the current round (releasing
  it early if the remaining arrivals already suffice); `Join` takes
  effect at the next round boundary and never disturbs the round in
  progress.
- **Failure propagation**: `Break(err)` or a failing task passed to
  `Run` fails the current round; every participant of that round
  observes the error, and the next round starts clean.
- **No crosstalk**: a generation counter tags every arrival, so a late
  arrival from a previous round can never leak into a new one.

## API

```go
b := rendezvous.New(3)   // 3 participants per round
b.Run(task)              // run task, then rendezvous; task failure breaks the round
b.Arrive()               // plain rendezvous
b.Break(err)             // inject a failure into the current round
b.Leave()                // stop participating, effective immediately
b.Join()                 // start participating from the next round
b.Parties(); b.Round()   // observability
```

## Test & demo

```sh
go test -race -v ./...   # includes a 1000-round no-crosstalk test
go run ./cmd/demo        # narrated run: failure, leave, join
```
