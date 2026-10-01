# Fairness Checker

Finite-state fairness model checker (Python 3.11+, standard library only).
For user-specified **weak-fair** and **strong-fair** actions it searches the
reachable state space for infinite-loop witnesses (lassos: prefix + cycle)
that violate the fairness constraint.

## Rules

- **Continuous enablement**: the action is enabled in *every* state of a loop.
- **Intermittent enablement**: enabled in *some but not all* loop states.
  The two are never conflated; each witness records which one applies.
- **Weak fairness** is violated by a loop where the action is *continuously*
  enabled but never executed.
- **Strong fairness** is violated by a loop where the action is enabled at all
  (continuously *or* intermittently) but never executed.
- Verdicts: `violated` (witness produced), `satisfied` (state space fully
  explored, no violating loop), `pending` (search truncated by `max_states`,
  so loops may remain unexamined — never reported as satisfied).
- Every counterexample is a `Witness` (prefix + closing loop) that `replay()`
  re-executes on the model, iterating the loop to prove it repeats forever
  and re-deriving enablement from the model rather than trusting the witness.

## Usage

```python
from fairness_checker import TransitionSystem, check_fairness, replay

model = TransitionSystem("t0", {
    "t0": [("b", "t0"), ("a", "t1")],
    "t1": [("b", "t0")],
})
result = check_fairness(model, weak=("a",), strong=("a",), max_states=None)
for r in result.results:
    print(r.kind.value, r.action, r.verdict.value, r.detail)
    if r.witness:
        print(replay(model, r.witness))  # re-verify the counterexample
```

Run the built-in demos and the test suite:

```
python3 fairness_checker.py
python3 -m unittest discover -v
```

## Sample run (`python3 fairness_checker.py`)

```
== Weak fairness violated (continuous enablement) ==
explored states: 2, exploration complete: True
[weak  ] action 'a': violated -- loop found where 'a' is continuously enabled but never executed
         enablement in loop: continuous
         prefix: (none)
         loop:   't0' -b-> 't0'   (repeats forever)
         replay: ok=True, violation confirmed=True, loop iterations checked=2
overall verdict: violated

== Intermittent enablement: weak holds, strong violated ==
explored states: 3, exploration complete: True
[weak  ] action 'a': satisfied -- no violating loop; state space fully explored
[strong] action 'a': violated -- loop found where 'a' is intermittently enabled but never executed
         enablement in loop: intermittent
         prefix: (none)
         loop:   's0' -b-> 's1' -b-> 's0'   (repeats forever)
         replay: ok=True, violation confirmed=True, loop iterations checked=2
overall verdict: violated

== Fairness satisfied (action executed in every loop) ==
explored states: 2, exploration complete: True
[weak  ] action 'a': satisfied -- no violating loop; state space fully explored
[strong] action 'a': satisfied -- no violating loop; state space fully explored
overall verdict: satisfied

== Truncated search stays pending ==
explored states: 1, exploration complete: False
[weak  ] action 'a': pending -- state space not fully explored (limit 1 reached)
overall verdict: pending
```

## Test results (`python3 -m unittest discover -v`)

14 tests, all passing:

```
test_sufficient_limit_completes (test_fairness_checker.PendingTests) ... ok
test_truncated_search_is_pending_not_satisfied (test_fairness_checker.PendingTests) ... ok
test_replay_confirms_witness_and_loop_repeats (test_fairness_checker.ReplayTests) ... ok
test_replay_rederives_enablement_not_witness_claim (test_fairness_checker.ReplayTests) ... ok
test_replay_rejects_non_closing_loop (test_fairness_checker.ReplayTests) ... ok
test_replay_rejects_tampered_transition (test_fairness_checker.ReplayTests) ... ok
test_replay_rejects_wrong_prefix_source (test_fairness_checker.ReplayTests) ... ok
test_strong_satisfied_when_never_enabled_in_any_loop (test_fairness_checker.StrongFairnessTests) ... ok
test_strong_violated_by_continuous_enablement_too (test_fairness_checker.StrongFairnessTests) ... ok
test_strong_violated_by_intermittent_enablement (test_fairness_checker.StrongFairnessTests) ... ok
test_unreachable_violating_loop_is_ignored (test_fairness_checker.WeakFairnessTests) ... ok
test_weak_not_violated_by_intermittent_enablement (test_fairness_checker.WeakFairnessTests) ... ok
test_weak_satisfied_when_action_executed (test_fairness_checker.WeakFairnessTests) ... ok
test_weak_violated_by_continuous_enablement (test_fairness_checker.WeakFairnessTests) ... ok

----------------------------------------------------------------------
Ran 14 tests in 0.001s

OK
```

## Files

- `fairness_checker.py` — checker library (`check_fairness`, `replay`,
  `TransitionSystem`) plus demo CLI.
- `test_fairness_checker.py` — unittest suite covering weak/strong
  violations, continuous-vs-intermittent distinction, pending on truncated
  search, and replay (including tamper rejection).
