# Temporal Property Checker

A temporal property checker for finite state machines with propositional
labels on states. It enumerates the finite state graph and verifies two
properties:

- **Liveness** `G(request -> F response)`: every request is eventually
  followed by a response.
- **Safety**: a `danger` state is only entered after an `authorized`
  state on the same run (authorization must hold strictly before).

## Usage

```sh
go build -o tchecker .
./tchecker -spec examples/ok.json
```

Flags (with defaults): `-request=request`, `-response=response`,
`-authorized=authorized`, `-danger=danger` select which propositions the
properties refer to. Exit code is 1 if any property fails, 2 on usage or
spec errors.

## Specification format

```json
{
  "initial": "idle",
  "states": [
    { "name": "idle",      "labels": [] },
    { "name": "requested", "labels": ["request"] }
  ],
  "transitions": [
    { "from": "idle", "to": "requested" }
  ]
}
```

## Semantics and algorithms

- **Reachability first.** Only states reachable from the initial state
  are considered; unreachable states never affect the verdicts.
- **Liveness via SCCs.** A violation of `G(request -> F response)`
  requires an infinite run on which a request stays pending forever.
  The checker restricts the reachable graph to response-free states,
  computes its strongly connected components (Tarjan), and looks for a
  cyclic SCC reachable from a reachable request state within that
  region. A finite path without a response is **not** a failure, because
  it may still be extended to a response.
- **Lasso witnesses.** On failure the witness is a finite prefix from
  the initial state to the cycle entry plus a closed loop
  (`first == last`) that never visits a response state.
- **Safety via restricted BFS.** The checker searches for a run that
  reaches a danger state while avoiding authorized states entirely.
  Such a run is a violation; the witness is the finite path. A danger
  label on a state that is itself authorized still counts as a
  violation unless authorization held strictly earlier.

## Tests

```sh
go test ./...
```

Covers: passing models, lasso witnesses, finite unresponsive paths not
counting as failures, self-loop violations, unauthorized danger,
authorized-before-danger, parallel-branch violations, danger at the
initial state, and unreachable states being ignored by both checks.
