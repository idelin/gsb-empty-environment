# multipaxos — fixed-membership Multi-Paxos core simulation

Five local simulated nodes replicate a command log. Standard library only;
no real networking — a discrete-event harness delivers, drops, duplicates,
reorders, crashes and restarts explicitly.

## Invariants implemented

- **Ballots** are `(round, nodeID)` pairs, totally ordered (`paxos/ballot.go`).
- **Acceptors persist** their max promise and per-slot accepted
  `(ballot, value)` before answering; chosen values are persisted too
  (`paxos/storage.go`, `paxos/node.go`). A failed write crashes the node
  before it can respond (persistence-failure model).
- **Phase 1**: a new leader collects 3 promises, then for every slot adopts
  the value of the highest accepted ballot in the quorum; slots with no
  accepted value are filled with `no-op`. Only after phase 1 completes may
  the leader propose later slots directly.
- **Chosen** = accepted by a majority (3 of 5). The application only
  advances the contiguous chosen prefix.
- **Restart** reloads promise/accepted/chosen from disk; stale-ballot
  messages are rejected (`<= maxPromise` for prepares, `< maxPromise` for
  accepts).
- **Independent checker** (`paxos/checker.go`) watches durable accepts and
  proves each chosen value from a majority; it flags any slot chosen with
  two values, any learn without majority evidence, and any bad apply.

## Layout

- `paxos/` — core: ballot, messages, disk, node (proposer/acceptor/learner),
  sim harness, checker, script runner.
- `cmd/replay/` — replays a script file and prints the trace, chosen
  evidence and node states.
- `examples/` — ready-made scripts (`recovery.txt`, `hole.txt`).
- `paxos/paxos_test.go` — scenario tests (minority old leader, chosen but
  notification lost, value behind a hole, repeated recovery, persistence
  failure, restart rejects old ballots), a seeded chaos safety test, and a
  stable-majority liveness test.

## Run

```sh
go test ./...
go run ./cmd/replay examples/recovery.txt
```

Script language (one event per line): `tick`, `campaign`, `propose`,
`deliver`, `drop`, `dup`, `deliver-all`, `drop-all`, `crash`, `restart`,
`diskfail`, `diskheal`, `expect-chosen`, `expect-not-chosen`,
`expect-applied`. See `paxos/script.go`.
