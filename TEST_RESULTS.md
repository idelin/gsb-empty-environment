# 测试结果

```text
$ python3.11 -m unittest discover -s tests -v
test_cancelled_scope_does_not_pollute_next_scope (test_solver.SolverTests.test_cancelled_scope_does_not_pollute_next_scope) ... ok
test_conflict_exit_leaves_outer_scope_solvable (test_solver.SolverTests.test_conflict_exit_leaves_outer_scope_solvable) ... ok
test_exception_inside_scope_rolls_back (test_solver.SolverTests.test_exception_inside_scope_rolls_back) ... ok
test_independent_checker_uses_original_domains_and_constraints (test_solver.SolverTests.test_independent_checker_uses_original_domains_and_constraints) ... ok
test_inner_derivations_remain_distinct_from_outer_level (test_solver.SolverTests.test_inner_derivations_remain_distinct_from_outer_level) ... ok
test_inner_search_conflict_leaves_outer_scope_solvable (test_solver.SolverTests.test_inner_search_conflict_leaves_outer_scope_solvable) ... ok
test_manual_cancel_scope_restores_previous_level (test_solver.SolverTests.test_manual_cancel_scope_restores_previous_level) ... ok
test_multiple_assumptions_in_one_scope (test_solver.SolverTests.test_multiple_assumptions_in_one_scope) ... ok
test_nested_derivations_are_attributed_and_rolled_back (test_solver.SolverTests.test_nested_derivations_are_attributed_and_rolled_back) ... ok
test_permanent_constraint_cannot_be_added_under_assumption (test_solver.SolverTests.test_permanent_constraint_cannot_be_added_under_assumption) ... ok
test_repeated_equal_assumptions_keep_separate_levels (test_solver.SolverTests.test_repeated_equal_assumptions_keep_separate_levels) ... ok
test_search_does_not_mutate_current_scope (test_solver.SolverTests.test_search_does_not_mutate_current_scope) ... ok
test_solution_is_independently_checked (test_solver.SolverTests.test_solution_is_independently_checked) ... ok

----------------------------------------------------------------------
Ran 13 tests in 0.116s

OK

$ python3.11 example.py
base propagation: True {'x': frozenset({2, 3}), 'y': frozenset({1, 2, 3}), 'z': frozenset({1, 2, 3})}
base removals: {0: [Removal(variable='x', value=1, level=0)]}
outer scope depth: 1
solution under y=3: {'x': 2, 'y': 3, 'z': 1}
domains after solve are not mutated: {'x': frozenset({2, 3}), 'y': frozenset({1, 2, 3}), 'z': frozenset({1, 2, 3})}
outer propagation: True {'x': frozenset({2}), 'y': frozenset({3}), 'z': frozenset({1})}
outer removals: {0: [Removal(variable='x', value=1, level=0)], 1: [Removal(variable='y', value=1, level=1), Removal(variable='y', value=2, level=1), Removal(variable='x', value=3, level=1), Removal(variable='z', value=3, level=1), Removal(variable='z', value=2, level=1)]}
inner scope depth: 2
conflicting x=3 propagation: False
after inner cancellation, outer solves: {'x': 2, 'y': 3, 'z': 1}
after outer cancellation: {'x': frozenset({2, 3}), 'y': frozenset({1, 2, 3}), 'z': frozenset({1, 2, 3})}
repeated equal assumptions retain nested depth: 2
solution through repeated assumptions: {'x': 2, 'y': 1, 'z': 3}
independent checker valid: True
independent checker invalid: False
independent checker with active assumption: True
```
