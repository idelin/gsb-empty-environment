# Bounded request-response completion checker

Checks, for every accepted request of a finite-state request-response
protocol, whether it can reach a terminal state — distinguishing
"possibly completes" from "completes on all fair paths" — and reports
blocked states. Python 3.11, standard library only.

## Files

- `model.py` — finite transition system + bounded may/must(fair) checker
  with replayable counterexamples.
- `protocol.py` — the request lifecycle model and the `RequestTracker`
  that owns per-request state.
- `run_sample.py` — runnable demo (`python3.11 run_sample.py`).
- `test_protocol.py` — unittest suite (`python3.11 -m unittest test_protocol -v`).

## Explicit rules

- **Terminal states**: `RESPONDED` (success) and `CANCELLED`. Cancellation
  is a declared terminal state: a cancelled request counts as complete.
- **Fairness**: `respond` is a fair action (weak fairness). An infinite
  path where a dispatched request is respondable forever but never
  responded to is unfair and excluded from the must-complete verdict.
- **Success path ≠ guarantee**: a reachable success path only proves
  may-complete; must-complete requires every fair path to terminate.
- **Duplicate request IDs**: each `accept()` creates an independent
  instance keyed by `(request_id, occurrence)`; states never interfere.
- **Bounded search**: exploration is limited to the given finite model
  and capped by `max_states`; if the bound is hit before the reachable
  space is exhausted, the verdict stays `PENDING` (no claim either way).
- **Counterexamples**: `BLOCKED` and `POSSIBLE_ONLY` verdicts carry a
  trace of `(action, target)` steps that `model.replay()` can re-execute;
  possible-only traces end in a cycle that may repeat forever.

## Verdicts

| Verdict          | Meaning                                              |
|------------------|------------------------------------------------------|
| `GUARANTEED`     | every fair path reaches a terminal state             |
| `POSSIBLE_ONLY`  | some path completes; some fair path never does       |
| `BLOCKED`        | reachable non-terminal dead end, or no terminal at all |
| `PENDING`        | search bound exhausted; verdict unknown              |
