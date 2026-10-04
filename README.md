# membersim

Discrete-event simulator for a SWIM-style group membership protocol.
Pure Go, no real network; `go test ./...` runs everything.

## Model

- **States**: `alive`, `suspect`, `dead`, plus a per-node `incarnation`.
- **Merge**: higher incarnation always wins; within one incarnation
  `dead > suspect > alive` (`mergeMembers` in member.go).
- **Incarnation ownership**: only the subject node itself may raise its
  own incarnation — by refuting a suspect/dead rumor about itself
  (`node.refute`) or by restarting, which loads the durable incarnation
  from `sim.persist` and increments it. Gossip never invents incarnations.
- **Probing**: direct probe timeout -> `NumIndirect` indirect probes via
  helpers -> only after the indirect deadline expires with no ack does the
  observer suspect the target. After `SuspicionTimeout` without a
  higher-incarnation refutation, the suspect is declared dead.
- **Generation tokens**: every timer carries a token — probe sequence
  numbers for probe/indirect timeouts, `(subject, incarnation)` for
  suspicion timers, `epoch` for protocol steps across restarts. Stale
  timers are logged and ignored, so an outdated timer can never override
  a refreshed `alive`.
- **Network**: messages may be dropped, duplicated and reordered
  (seeded RNG); partitions cut links until healed. Crashed nodes drop
  inbound traffic; restarted nodes rejoin with `persist+1` and push a
  fresh `alive`.

## Determinism

Events are processed in total order `(tick, eventID)` from a single
priority queue; all randomness comes from one seeded RNG. A run is fully
determined by `Config.Seed` plus the fault plan (`[]Fault`), so any run
can be replayed event by event (see `TestDeterministicReplay`).

## Output

`sim.Transitions()` — ordered view changes with reasons;
`sim.Logs()` — verbose event log (stale timers, probe phases, faults);
`sim.View(id)` — final membership view per node; `sim.Report()` renders
all of the above.

## Tests

Late ack, refutation inside the suspicion window, stale suspicion timers,
partition/heal recovery, convergence without loss, restart rejoin,
incarnation-ownership invariants, deterministic replay, and an
independent tuple-max model cross-check of the merge function.
