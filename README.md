# cexshrink — replayable counterexample shrinking in a single-process event simulator

A naive two-phase-commit protocol (coordinator `C`, participants `P1`, `P2`)
is simulated in a single process: message delivery, packet loss and timeouts
are all explicit events in a schedule trace. The protocol bug: a prepared
participant **aborts unilaterally on timeout**, so a lost `Commit` plus a
timeout splits the decisions (`C,P1` commit vs `P2` abort).

A trace is a *complete* (quiescent) schedule: every sent message must be
either delivered or explicitly dropped, and every event must be executable
(delivering a message that was never sent is a broken dependency). This makes
packet loss a first-class, load-bearing ingredient of a counterexample.

`Shrink` greedily removes events while the **exact same safety-violation
signature** is preserved:

- a candidate that no longer replays (broken dependency / not quiescent) is rejected;
- a candidate that no longer fails — or only leaves a node stuck after a
  timeout (a liveness symptom, never the same safety error) — is rejected;
- a necessary loss is therefore kept, since removing it breaks the failure.

Evidence is produced separately for **safety** (the minimal trace replays to
divergent decisions) and **liveness** (the fault-free schedule terminates
with all nodes committing).

## Run

```sh
go run .        # prints original trace, shrink log, minimal counterexample, evidence
go test ./...   # simulator + shrinker tests
```

## Files

- `sim.go` — event simulator, deterministic replay, violation classifier
- `shrink.go` — greedy 1-minimal shrinker with dependency/quiescence validation
- `main.go` — driver: original trace, shrink log, minimal counterexample, evidence
- `sim_test.go`, `shrink_test.go` — tests
