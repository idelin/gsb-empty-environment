# Counterexample Shrinker for Finite-State Models

Python 3.11, standard library only (`unittest` for tests).

Given a **fixed** finite-state model and a counterexample triple
`(initial config, action trace, fault set)` that violates a property,
`shrinker.shrink` greedily deletes config entries, trace actions and faults.
Every candidate is **replayed independently from scratch** and is accepted
only if replay triggers the **same** violation (identical error signature):

* a deletion that breaks an action precondition is rejected
  (`precondition_failed` is never accepted);
* a different error signature is a different counterexample and is rejected;
* when all single deletions are explored and rejected, the result is reported
  as `local_minimum` (1-minimal);
* if the replay budget (`max_replays`) runs out first, the result stays
  `undecided` — no minimality claim is made, and global minimality is never
  claimed;
* the returned triple always replays to the original violation.

## Files

* `shrinker.py` — model protocol, independent replay, greedy shrinker.
* `wallet_model.py` — demo model: a wallet with overdraft plus injectable
  faults (`lost_deposit`, `double_withdraw`); violations
  `NEGATIVE_BALANCE` / `OVERDRAFT_BEYOND_LIMIT`.
* `run_demo.py` — sample run.
* `test_shrinker.py` — unittest suite.

## Run sample

```
$ python3.11 run_demo.py
--- Original counterexample ---
  config: {'balance': 10, 'limit': 5, 'overdraft': True, 'log_level': 'debug'}
  trace : [('deposit', 4), ('set_limit', 8), ('withdraw', 3), ('deposit', 2), ('withdraw', 7), ('disable_overdraft',), ('enable_overdraft',), ('withdraw', 9)]
  faults: ['double_withdraw', 'lost_deposit']
  replay: outcome=violation signature=OVERDRAFT_BEYOND_LIMIT step=4

--- Shrunk counterexample ---
  config: {'balance': 10, 'overdraft': True}
  trace : [('set_limit', 8), ('withdraw', 3), ('withdraw', 9)]
  faults: ['double_withdraw']
  verification replay: outcome=violation signature=OVERDRAFT_BEYOND_LIMIT step=2
  status: local_minimum (replays used: 21)

--- Same search with a tight replay budget (max_replays=8) ---
  status: undecided (replays used: 8)
  partial result still replays: outcome=violation signature=OVERDRAFT_BEYOND_LIMIT
```

## Test results

```
$ python3.11 -m unittest -v
test_clean_run (test_shrinker.ReplayTests.test_clean_run) ... ok
test_initial_counterexample_violates (test_shrinker.ReplayTests.test_initial_counterexample_violates) ... ok
test_precondition_failure_detected (test_shrinker.ReplayTests.test_precondition_failure_detected) ... ok
test_replay_is_deterministic (test_shrinker.ReplayTests.test_replay_is_deterministic) ... ok
test_budget_exhaustion_stays_undecided (test_shrinker.ShrinkTests.test_budget_exhaustion_stays_undecided) ... ok
test_different_error_is_not_same_counterexample (test_shrinker.ShrinkTests.test_different_error_is_not_same_counterexample) ... ok
test_fault_set_shrunk_to_essential_fault (test_shrinker.ShrinkTests.test_fault_set_shrunk_to_essential_fault) ... ok
test_invalid_budget_rejected (test_shrinker.ShrinkTests.test_invalid_budget_rejected) ... ok
test_irrelevant_config_entry_removed (test_shrinker.ShrinkTests.test_irrelevant_config_entry_removed) ... ok
test_local_minimum_found (test_shrinker.ShrinkTests.test_local_minimum_found) ... ok
test_non_violating_initial_input_rejected (test_shrinker.ShrinkTests.test_non_violating_initial_input_rejected) ... ok
test_precondition_breaking_deletion_rejected (test_shrinker.ShrinkTests.test_precondition_breaking_deletion_rejected) ... ok
test_result_is_one_minimal (test_shrinker.ShrinkTests.test_result_is_one_minimal) ... ok
test_result_replays_to_same_violation (test_shrinker.ShrinkTests.test_result_replays_to_same_violation) ... ok

----------------------------------------------------------------------
Ran 14 tests in 0.001s

OK
```
