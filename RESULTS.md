# Test results

```
UTC: 2026-09-25 05:51
Python 3.11.15

test_batch_with_flags_validates_atomically (test_engine.BatchPublishTests.test_batch_with_flags_validates_atomically) ... ok
test_validate_before_publish_keeps_good_state (test_engine.BatchPublishTests.test_validate_before_publish_keeps_good_state) ... ok
test_cancelled_staging_leaves_state_untouched (test_engine.CancellationTests.test_cancelled_staging_leaves_state_untouched) ... ok
test_retry_after_cancel_converges (test_engine.CancellationTests.test_retry_after_cancel_converges) ... ok
test_checkpoint_preserves_error_state_and_cancel_consistency (test_engine.CheckpointTests.test_checkpoint_preserves_error_state_and_cancel_consistency) ... ok
test_enum_and_bigint_round_trip (test_engine.CheckpointTests.test_enum_and_bigint_round_trip) ... ok
test_restore_is_consistent_and_remains_incremental (test_engine.CheckpointTests.test_restore_is_consistent_and_remains_incremental) ... ok
test_condition_switch_deletes_old_dependency (test_engine.ConditionCycleTests.test_condition_switch_deletes_old_dependency) ... ok
test_toggle_creates_then_removes_cycle (test_engine.ConditionCycleTests.test_toggle_creates_then_removes_cycle) ... ok
test_downstream_only_constant_does_not_retrigger_leaf (test_engine.IncrementalTests.test_downstream_only_constant_does_not_retrigger_leaf) ... ok
test_initial_evaluation_counts_all_constants (test_engine.IncrementalTests.test_initial_evaluation_counts_all_constants) ... ok
test_no_change_means_no_evaluation (test_engine.IncrementalTests.test_no_change_means_no_evaluation) ... ok
test_unrelated_module_is_not_evaluated (test_engine.IncrementalTests.test_unrelated_module_is_not_evaluated) ... ok
test_local_constants_are_not_exported (test_engine.ModuleIsolationTests.test_local_constants_are_not_exported) ... ok
test_same_named_constants_do_not_misbind (test_engine.ModuleIsolationTests.test_same_named_constants_do_not_misbind) ... ok
test_indexing_and_out_of_range (test_language.ArrayTests.test_indexing_and_out_of_range) ... ok
test_modulo_zero (test_language.DivisionByZeroTests.test_modulo_zero) ... ok
test_zero_source_chain_and_dependent_diagnostic (test_language.DivisionByZeroTests.test_zero_source_chain_and_dependent_diagnostic) ... ok
test_cross_module_enum_reference (test_language.EnumTests.test_cross_module_enum_reference) ... ok
test_enum_values_and_arithmetic (test_language.EnumTests.test_enum_values_and_arithmetic) ... ok
test_error_mode_reports_and_keeps_last_good (test_language.OverflowTests.test_error_mode_reports_and_keeps_last_good) ... ok
test_exact_mode_allows_arbitrary_precision (test_language.OverflowTests.test_exact_mode_allows_arbitrary_precision) ... ok
test_policy_change_invalidates_fingerprint (test_language.OverflowTests.test_policy_change_invalidates_fingerprint) ... ok
test_wrap_mode_is_signed_64bit (test_language.OverflowTests.test_wrap_mode_is_signed_64bit) ... ok
test_forward_reference (test_language.ParsingTests.test_forward_reference) ... ok
test_parse_error_is_diagnostic (test_language.ParsingTests.test_parse_error_is_diagnostic) ... ok
test_precedence_and_unary_and_modulo (test_language.ParsingTests.test_precedence_and_unary_and_modulo) ... ok
test_diagnostic_span_points_at_source_bytes (test_language.SpanTests.test_diagnostic_span_points_at_source_bytes) ... ok
test_spans_are_utf8_byte_offsets (test_language.SpanTests.test_spans_are_utf8_byte_offsets) ... ok

----------------------------------------------------------------------
Ran 29 tests in 0.020s

OK
```

# Demo transcript ($ python3.11 demo.py)

```

========================================================================
1. Forward reference: A = B + 1, B = 2  =>  A = 3
========================================================================
  A = 3  B = 2

========================================================================
2. Switch 'fast' on: B = A introduces a new cycle
========================================================================
  M:[16,17): cycle: definition cycle: M.A -> M.B -> M.A
      via M.B [74,75)
  M:[74,75): cycle: definition cycle: M.B -> M.A -> M.B
      via M.A [16,17)
  last-good artifacts are NOT polluted: A = 3  B = 2

========================================================================
3. Switch 'fast' back off: diagnostics revoked
========================================================================
  diagnostics: (none)
  A = 3  B = 2

========================================================================
4. Same-named constants do not misbind; locals are not exported
========================================================================
  M.pub = 101  N.ok = 103  A.r = 3
  N:[67,75): name-error: 'M.secret' is local to module 'M' and is not exported

========================================================================
5. Enums, arrays and indexed references
========================================================================
  Color.Blue = EnumVal(enum='Color', member='Blue', value=6)
  a[2] + Color.Green = 35

========================================================================
6. Batch modify: validate first, publish only when clean
========================================================================
  before: B = 2
  rejected batch committed? False
  M:[50,55): div-by-zero: division by zero
      via zero divisor originated at M.Z [29,30)
  published B still = 2
  fixed batch committed? True  B = 42

========================================================================
7. Dependency fingerprint: editing M never evaluates N
========================================================================
  initial eval_count = 5
  after editing M: evaluated = ['M.a', 'M.b', 'P.q']
                    reused    = ['N.x', 'N.y']

========================================================================
8. Integer overflow policies (signed 64-bit)
========================================================================
  exact: X = 9223372036854775808
  wrap : X = -9223372036854775808
  error: overflow diagnostic (artifact unavailable / last-good kept)

========================================================================
9. Cancellation leaves state intact; checkpoint restores exactly
========================================================================
  cancelled apply raised Cancelled; A still = 3
  artifacts after restore equal engine state: True
  post-restore evaluated sets identical: True -> ['M.A', 'M.B']
  post-restore artifacts identical: True
```
