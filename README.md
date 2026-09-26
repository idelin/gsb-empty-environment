# Finite-State Model Checker with Independent Certificate Verification

A small, dependency-free model checker for **finite-state safety properties**
(Python 3.11, standard library only). The checker exports machine-checkable
certificates, and a separate verifier re-derives every claim from the model
itself — it never trusts the generator's statistics.

## Guarantees

- **Reachability-closure certificate for safety**: lists every reachable
  state and the full successor set of each. The verifier independently checks
  - *initial coverage*: every initial state is in the closure;
  - *successor closure*: every declared successor of every listed state is
    listed too;
  - *the property*: no listed state satisfies the bad-state predicate;
  - *completeness*: it re-runs reachability from the initial states against
    the model's own transition table, so hidden reachable states are caught.
- **Counterexample certificate for violations**: an initial-to-bad path plus
  per-step transition records. The verifier replays every step against the
  model's transition table and pinpoints any invalid transition by its index
  (`source`, `target`, and the actually available successors).
- **No certificate on partial search**: when the BFS expansion budget is
  exhausted before the frontier closes, the result is `unknown` (exit code
  3) and nothing is emitted. A partial exploration graph cannot be packaged
  as a complete safety certificate; such payloads are rejected.
- **Model binding**: every certificate carries the SHA-256 fingerprint of a
  canonical encoding of the model; verification against a different model
  fails with `FINGERPRINT_MISMATCH`.
- **Stats are advisory only**: `generator_stats` is never consulted by the
  verifier (a test forges wrong counts and still gets the correct verdict).

Only the given finite model is examined — no theorem proving, no search
outside the declared state set.

## Layout

```
src/modelchecker/model.py     FiniteModel: states, initial set, transition table, fingerprint
src/modelchecker/checker.py   BFS check_safety() with optional budget; certificate builders
src/modelchecker/verifier.py  Independent verifiers for closures and counterexamples
src/modelchecker/models.py    Example models + JSON model loader
src/modelchecker/cli.py       `check`, `verify`, `list` commands
tests/test_modelchecker.py    27 unittest cases
examples/                     Pre-generated certificates, demo + test transcripts
```

## Quick start

```sh
export PYTHONPATH=src

python3.11 -m modelchecker.cli list
python3.11 -m modelchecker.cli check two_bit_counter -o safe.json
python3.11 -m modelchecker.cli verify two_bit_counter safe.json

./run_demo.sh      # end-to-end scenario walkthrough
./run_tests.sh     # full unittest suite
```

Exit codes: `0` decided-safe / valid certificate, `1` invalid certificate,
`2` decided-unsafe (counterexample emitted), `3` unknown (budget exhausted).

A custom model can be supplied as JSON (transition keys are JSON-encoded
states):

```json
{
  "name": "custom_loop",
  "states": ["a", "b"],
  "initial": ["a"],
  "transitions": {"\"a\"": ["b"], "\"b\"": ["a"]},
  "bad_states": []
}
```

## Required adversarial scenarios

All three are demonstrated in `./run_demo.sh` (transcript:
`examples/demo_output.txt`) and locked down by tests:

1. **Delete a required successor state** — removing state `2` from state
   `1`'s successor list yields `SUCCESSOR_NOT_CLOSED` with
   `{state: 1, missing_successor: 2}`; deleting the state itself is also
   caught independently as `REACHABILITY_INCOMPLETE`.
2. **Bad counterexample transition is localized** — forging path
   `[0, 1, 3]` yields `BAD_TRANSITION` at `transition_index: 1`
   (`source: 1, target: 3, available: [2]`) plus `END_NOT_BAD`.
3. **Budget-limited partial graph cannot be a complete safety certificate**
   — the checker returns `unknown` without a certificate; a hand-forged
   partial closure fails both `SUCCESSOR_NOT_CLOSED` and
   `REACHABILITY_INCOMPLETE`; a `partial_exploration` payload fails with
   `UNSUPPORTED_KIND`.

## Sample run

The full transcript is in `examples/demo_output.txt`; highlights:

### Safe model — valid closure

```json
{
  "verdict": "valid",
  "certificate_kind": "reachability_closure",
  "findings": []
}
```

### Unsafe model — replayed counterexample

```text
UNSAFE: counterexample path [0, 1, 2]
```
```json
{
  "verdict": "valid",
  "certificate_kind": "counterexample",
  "findings": []
}
```

### Budget exhausted — stays unknown, no certificate

```json
{
  "outcome": "unknown",
  "explored": 4,
  "expansions": 3,
  "budget": 3,
  "reason": "budget exhausted before the reachable set was closed"
}
```

### Deleted required successor — certificate fails

```json
{
  "verdict": "invalid",
  "findings": [
    {
      "code": "SUCCESSOR_NOT_CLOSED",
      "detail": {"state": 1, "missing_successor": 2}
    }
  ]
}
```

## Test results

`./run_tests.sh` (Python 3.11.15), complete transcript in
`examples/test_results.txt`:

```text
Ran 27 tests in 0.07s

OK
```

The suite covers: complete/incomplete BFS, budget boundaries (including
budget 0 with multiple initial states), unreachable bad states, closure
tampering (missing successor/state/initial, phantom successor, injected bad
state), counterexample tampering (bad transition localization, non-initial
start, length mismatch), fingerprint mismatch, forged generator stats,
partial-payload rejection, and CLI round-trips including a JSON-file model.
