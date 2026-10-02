# 实际运行结果

环境: Python 3.11.15, macOS (arm64)。运行日期: 2026-10-02。

## 测试套件

```
$ python3.11 -m unittest discover -v
test_cancel_then_checkpoint_resume_converges (tests.test_cg.TestCheckpointAndVersions.test_cancel_then_checkpoint_resume_converges) ... ok
test_matrix_version_change_rejects_stale_search_directions (tests.test_cg.TestCheckpointAndVersions.test_matrix_version_change_rejects_stale_search_directions) ... ok
test_matches_exact_elimination_on_small_spd (tests.test_cg.TestDenseComparison.test_matches_exact_elimination_on_small_spd) ... ok
test_exact_x0_returns_in_zero_iterations (tests.test_cg.TestExactInitialGuess.test_exact_x0_returns_in_zero_iterations) ... ok
test_failing_matvec_then_restart_from_partial_x (tests.test_cg.TestFailureAndRestart.test_failing_matvec_then_restart_from_partial_x) ... ok
test_termination_uses_true_residual (tests.test_cg.TestIllConditionedTrueResidual.test_termination_uses_true_residual) ... ok
test_max_iter_returns_best_iterate_not_a_failure (tests.test_cg.TestMaxIterIsNotNoSolution.test_max_iter_returns_best_iterate_not_a_failure) ... ok
test_asymmetric_input_returns_numeric_evidence (tests.test_cg.TestNonSPDInputs.test_asymmetric_input_returns_numeric_evidence) ... ok
test_indefinite_matrix_returns_numeric_evidence (tests.test_cg.TestNonSPDInputs.test_indefinite_matrix_returns_numeric_evidence) ... ok
test_diagonal_preconditioner_update_and_exactness (tests.test_cg.TestPreconditioner.test_diagonal_preconditioner_update_and_exactness) ... ok
test_indefinite_preconditioner_detected_with_evidence (tests.test_cg.TestPreconditioner.test_indefinite_preconditioner_detected_with_evidence) ... ok
test_zero_diagonal_entries (tests.test_cg.TestPreconditioner.test_zero_diagonal_entries) ... ok
test_warm_start_after_matrix_version_change (tests.test_cg.TestWarmStartReinitializesKrylov.test_warm_start_after_matrix_version_change) ... ok

----------------------------------------------------------------------
Ran 13 tests in 0.071s

OK
```

## 病态 SPD 上递推残差与真实残差的分离

对角 SPD（特征值簇 [1,10] + 离群值 1e4..1e8，kappa=1e8），rtol=1e-15：

```
status: stagnated | iterations: 169
tolerance:                5.686e-15
final true residual:      7.798e-14
final recursive residual: 1.457e-15
divergence points (recursive <= tol < true): 7, max gap 218.8x
  k= 160  recursive=2.337e-15  true=7.796e-14  gap=33.4x
  k= 162  recursive=5.115e-15  true=7.822e-14  gap=15.3x
  k= 163  recursive=9.928e-16  true=7.823e-14  gap=78.8x
```

递推残差降至容差（5.7e-15）以下时，真实残差仍停滞在 ~7.8e-14（最大差距 218 倍）。
求解器未误报收敛，而是以 stagnated 终止，并返回真实残差 checkpoint 序列作为证据。
