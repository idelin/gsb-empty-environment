# 实际运行结果

日期：2026-09-21 20:18:16 CST；解释器：Python 3.11.15

## `python3.11 -m unittest discover -s tests -v`

```
test_prerelease_not_selected_without_channel (test_resolver.BasicResolutionTest.test_prerelease_not_selected_without_channel) ... ok
test_reasons_track_excluding_constraints (test_resolver.BasicResolutionTest.test_reasons_track_excluding_constraints) ... ok
test_resolves_highest_compatible (test_resolver.BasicResolutionTest.test_resolves_highest_compatible) ... ok
test_target_conditional_dependency (test_resolver.BasicResolutionTest.test_target_conditional_dependency) ... ok
test_adjacent_open_boundary_in_fixture (test_resolver.CycleTest.test_adjacent_open_boundary_in_fixture) ... ok
test_compatible_cycle_resolves (test_resolver.CycleTest.test_compatible_cycle_resolves) ... ok
test_conflicting_cycle_is_unsatisfiable_with_explanation (test_resolver.CycleTest.test_conflicting_cycle_is_unsatisfiable_with_explanation) ... ok
test_cycle_cross_validated_by_brute_force (test_resolver.CycleTest.test_cycle_cross_validated_by_brute_force) ... ok
test_lock_change_attributes_causing_constraints (test_resolver.LockChangeTest.test_lock_change_attributes_causing_constraints) ... ok
test_lock_pins_version (test_resolver.LockChangeTest.test_lock_pins_version) ... ok
test_explanation_names_constraints_and_versions (test_resolver.UnsatisfiableTest.test_explanation_names_constraints_and_versions) ... ok
test_adjacent_open_boundaries_keep_the_hole (test_versionset.BoundaryTest.test_adjacent_open_boundaries_keep_the_hole) ... ok
test_unbounded_endpoints (test_versionset.BoundaryTest.test_unbounded_endpoints) ... ok
test_counterexample_respects_channels (test_versionset.CounterexampleTest.test_counterexample_respects_channels) ... ok
test_minimal_counterexample (test_versionset.CounterexampleTest.test_minimal_counterexample) ... ok
test_difference_first_half (test_versionset.DifferenceTest.test_difference_first_half) ... ok
test_excluded_points (test_versionset.DifferenceTest.test_excluded_points) ... ok
test_associativity_commutativity (test_versionset.NormalizationOrderTest.test_associativity_commutativity) ... ok
test_difference_laws (test_versionset.NormalizationOrderTest.test_difference_laws) ... ok
test_distributive_laws (test_versionset.NormalizationOrderTest.test_distributive_laws) ... ok
test_renormalization_is_idempotent (test_versionset.NormalizationOrderTest.test_renormalization_is_idempotent) ... ok
test_prerelease_min_version_uses_channel (test_versionset.PrereleaseChannelTest.test_prerelease_min_version_uses_channel) ... ok
test_prerelease_only_when_channel_allowed (test_versionset.PrereleaseChannelTest.test_prerelease_only_when_channel_allowed) ... ok
test_same_numeric_parts_do_not_leak (test_versionset.PrereleaseChannelTest.test_same_numeric_parts_do_not_leak) ... ok
test_hash_is_canonical_and_stable (test_versionset.SerializationTest.test_hash_is_canonical_and_stable) ... ok
test_round_trip (test_versionset.SerializationTest.test_round_trip) ... ok
test_target_algebra (test_versionset.TargetTest.test_target_algebra) ... ok
test_target_conditions_gate_membership (test_versionset.TargetTest.test_target_conditions_gate_membership) ... ok

----------------------------------------------------------------------
Ran 28 tests in 0.013s

OK
```

## 求解器行为演示（`python3.11 examples/demo.py`）

```
== 锁版本变更追踪 (lib: 1.5.0 -> 锁定 1.2.0) ==
  lib: 1.5.0 -> 1.2.0
    caused_by: app@1.1.0 requires lib {"components": [{"channels": {"only": ["stable"]}, "intervals": ["[1.2.0, 2.0.0)"], "targets": {"all": true}}]}
  util: 0.6.0 -> 0.9.0
    caused_by: lib@1.2.0 requires util {"components": [{"channels": {"only": ["stable"]}, "intervals": ["[0.5.0, 1.0.0)"], "targets": {"all": true}}]}

== 不可满足解释（循环冲突夹具） ==
cannot select a version of 'a':
  - root requires a {"components": [{"channels": {"only": ["stable"]}, "intervals": ["[1.0.0, +inf)"], "targets": {"all": true}}]}
  - b@2.0.0 requires a {"components": [{"channels": {"only": ["stable"]}, "intervals": ["[0.1.0, 1.0.0)"], "targets": {"all": true}}]}
  => the constraints are mutually exclusive (empty intersection)
  available versions in repo: 1.0.0
  note: already selected a@1.0.0 violates it

== 最小反例与规范哈希 ==
  [1.0.0,2.0.0) vs [1.0.0,2.0.0] -> 2.0.0
  规范哈希: 5a088e5dfd9a33146c41bbbb1a6e7b691f3506b1299577193bba8eb38ae0a222
```
