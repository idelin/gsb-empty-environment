# 实际运行结果

```
$ python3.11 -m unittest discover -s tests -v
test_add_sub_exact (test_affine.TestCorrelation.test_add_sub_exact) ... ok
test_independent_vars_same_interval_do_not_cancel (test_affine.TestCorrelation.test_independent_vars_same_interval_do_not_cancel) ... ok
test_shared_symbol_cancels_across_nodes (test_affine.TestCorrelation.test_shared_symbol_cancels_across_nodes) ... ok
test_x_minus_x_is_exact_zero (test_affine.TestCorrelation.test_x_minus_x_is_exact_zero) ... ok
test_mul_remainder_conservative (test_affine.TestNonlinearRemainders.test_mul_remainder_conservative) ... ok
test_recip_interval_containing_zero_raises (test_affine.TestNonlinearRemainders.test_recip_interval_containing_zero_raises) ... ok
test_recip_min_range_bound (test_affine.TestNonlinearRemainders.test_recip_min_range_bound) ... ok
test_recip_negative_interval (test_affine.TestNonlinearRemainders.test_recip_negative_interval) ... ok
test_budget_exceeded_returns_incomplete (test_budget.TestBudget.test_budget_exceeded_returns_incomplete) ... ok
test_within_budget_is_complete (test_budget.TestBudget.test_within_budget_is_complete) ... ok
test_json_round_trip (test_certificate.TestCertificate.test_json_round_trip) ... ok
test_tampered_form_rejected (test_certificate.TestCertificate.test_tampered_form_rejected) ... ok
test_tampered_interval_rejected (test_certificate.TestCertificate.test_tampered_interval_rejected) ... ok
test_tampered_structure_rejected (test_certificate.TestCertificate.test_tampered_structure_rejected) ... ok
test_tampered_symbol_radius_rejected (test_certificate.TestCertificate.test_tampered_symbol_radius_rejected) ... ok
test_valid_certificate_verifies (test_certificate.TestCertificate.test_valid_certificate_verifies) ... ok
test_wrong_format_rejected (test_certificate.TestCertificate.test_wrong_format_rejected) ... ok
test_divisor_crossing_zero (test_divzero.TestDivisionByZeroInterval.test_divisor_crossing_zero) ... ok
test_safe_divisor_is_fine (test_divzero.TestDivisionByZeroInterval.test_safe_divisor_is_fine) ... ok
test_dense_explicit_size_checks (test_enumeration.TestEnumeration.test_dense_explicit_size_checks) ... ok
test_dense_matvec_containment (test_enumeration.TestEnumeration.test_dense_matvec_containment) ... ok
test_full_chain_containment (test_enumeration.TestEnumeration.test_full_chain_containment) ... ok
test_certificate_stable_after_incremental_change (test_incremental.TestIncremental.test_certificate_stable_after_incremental_change) ... ok
test_intervals_update_downstream (test_incremental.TestIncremental.test_intervals_update_downstream) ... ok
test_only_affected_successors_recomputed (test_incremental.TestIncremental.test_only_affected_successors_recomputed) ... ok
test_abs_crossing_kink_includes_kink_not_just_endpoints (test_piecewise.TestKinkCrossing.test_abs_crossing_kink_includes_kink_not_just_endpoints) ... ok
test_custom_pl_range_scans_kinks (test_piecewise.TestKinkCrossing.test_custom_pl_range_scans_kinks) ... ok
test_envelope_drops_correlation_but_stays_sound (test_piecewise.TestKinkCrossing.test_envelope_drops_correlation_but_stays_sound) ... ok
test_no_kink_is_exact_and_preserves_correlation (test_piecewise.TestKinkCrossing.test_no_kink_is_exact_and_preserves_correlation) ... ok
test_out_of_domain_is_explicit_error (test_piecewise.TestKinkCrossing.test_out_of_domain_is_explicit_error) ... ok
test_pl_spec_validation (test_piecewise.TestKinkCrossing.test_pl_spec_validation) ... ok
test_relu_crossing_kink (test_piecewise.TestKinkCrossing.test_relu_crossing_kink) ... ok

----------------------------------------------------------------------
Ran 32 tests in 0.006s

OK
```

运行环境: Python 3.11.15, macOS (arm64), 仅标准库。
