# 测试结果

环境：`Python 3.11.15`，仅标准库。

## 单元测试与穷举对照 (`python3.11 run_tests.py`)

```
test_add_and_delete_rule_support_retraction (test_core.AutomatonTests.test_add_and_delete_rule_support_retraction) ... ok
test_alternate_justification_keeps_state (test_core.AutomatonTests.test_alternate_justification_keeps_state) ... ok
test_deep_edit_touches_only_spine (test_core.AutomatonTests.test_deep_edit_touches_only_spine) ... ok
test_limits_and_cancellation (test_core.AutomatonTests.test_limits_and_cancellation) ... ok
test_occurrence_paths_distinguish_positions (test_core.AutomatonTests.test_occurrence_paths_distinguish_positions) ... ok
test_shared_node_evaluated_once (test_core.AutomatonTests.test_shared_node_evaluated_once) ... ok
test_snapshot_is_immutable_history (test_core.AutomatonTests.test_snapshot_is_immutable_history) ... ok
test_states_and_accepting (test_core.AutomatonTests.test_states_and_accepting) ... ok
test_operators_and_combinators (test_core.ConditionsTests.test_operators_and_combinators) ... ok
test_alphabet_enforced_and_hash_cons_shares (test_core.DagTests.test_alphabet_enforced_and_hash_cons_shares) ... ok
test_self_loop_and_dangling_refs (test_core.DagTests.test_self_loop_and_dangling_refs) ... ok
test_structural_cycles_rejected_with_witness (test_core.DagTests.test_structural_cycles_rejected_with_witness) ... ok
test_ungrounded_cycle_cannot_self_support (test_core.FixpointSemanticsTests.test_ungrounded_cycle_cannot_self_support) ... ok
test_arity_is_child_count (test_core.RuleTests.test_arity_is_child_count) ... ok
test_signature_filter (test_core.RuleTests.test_signature_filter) ... ok
test_editing_leaf_to_other_label (test_exhaustive.ExhaustiveTests.test_editing_leaf_to_other_label) ... ok
test_fixpoint_mode_matches_topological_and_reference (test_exhaustive.ExhaustiveTests.test_fixpoint_mode_matches_topological_and_reference) ... ok
test_leaf_edits_only_change_spine_and_match_reference (test_exhaustive.ExhaustiveTests.test_leaf_edits_only_change_spine_and_match_reference) ... ok
test_rule_operations_match_reference (test_exhaustive.ExhaustiveTests.test_rule_operations_match_reference) ... ok

----------------------------------------------------------------------
Ran 19 tests in 0.007s

OK
```

## 可重放样例 (`examples/demo.py`)

```

========================================================================
1. Shared-subtree DAG evaluated bottom-up, one pass
========================================================================
state table:
  node 0 (a/0): ['qA', 'qLeaf']
  node 1 (a/0): ['qLeaf']
  node 2 (b/2): ['qB']
  node 3 (b/2): ['qRoot']
  node 4 (b/2): ['qRoot']
  node 5 (k/1): []
  node 6 (x/0): []
  node 7 (y/1): []
rule-match attempts for the whole DAG: 10
accepting: {'left': ['qRoot'], 'right': ['qRoot']}

========================================================================
2. Same subtree id=0, two parent occurrences -> distinct evidence
========================================================================
qB proofs at node 2: 1
  via red-pair: child occurrence paths ['$[0]', '$[1]'], child node ids [0, 0]

========================================================================
3. Same label 'k' but different arity must not mix
========================================================================
node 5 is k/1: ['qK1']
qK2 (binary) never applies to k/1: True

========================================================================
4. Deep leaf edit: only the leaf and ancestors recompute
========================================================================
edit report: EditReport(path=(1,), old_leaf=1, new_leaf=0, rebuilt=(4, 0), changed=True, accepting_before=frozenset({'qRoot'}), accepting_after=frozenset({'qRoot'}))
rule-match attempts during edit: 4 (rebuilt spine ids [4, 0])
shared node 2 state cache retained: ['qB']

========================================================================
5. Old snapshot still answers queries after the edit
========================================================================
snapshot v1 root 'left' accepting: ['qRoot']
snapshot v1 still yields qRoot witnesses: 1
live accepting after edit: {'left': ['qRoot'], 'right': ['qRoot']}

========================================================================
6. Rule addition then deletion with support-based retraction
========================================================================
after adding blue-a, node 1: ['qA', 'qLeaf']
root 'left' accepting now: ['qRoot']
after deleting blue-a, node 1: ['qLeaf']
root 'left' accepting restored: ['qRoot']

========================================================================
6b. Alternate justification keeps a state; second delete drops it
========================================================================
qB still supported by copy: True
qB gone after the copy is also deleted: True
qRoot retracted transitively at roots: True

========================================================================
7. Rule cycles: least fixpoint never grounds unsupported states
========================================================================
with only qY:- qX, nothing is derived: {10: set(), 11: set()}
once x/0 grounds qX, qY appears: {10: {'qX'}, 11: {'qY'}}
deleting the ground rule empties the cycle: {10: set(), 11: set()}

========================================================================
8. Resource limits and cancellation
========================================================================
max_firings raised: max_firings=2 exceeded
countdown token raised: evaluation cancelled
a fresh token lets evaluation finish: True

========================================================================
Summary
========================================================================
snapshots kept: [1, 2]
DEMO OK
```

## 反例 (`examples/counterexamples.py`)

```
PASS  structural cycle rejected  structural cycle detected: 0 -> 1 -> 0
PASS  self loop rejected  
PASS  unary rule only fires at k/1  
PASS  binary rule only fires at k/2  
PASS  nullary rule never matches k/1  
PASS  pure rule cycle derives nothing  states={0: set(), 1: set()} rounds ground at empty
PASS  naive all-state seed incorrectly 'accepts'  
PASS  adding a ground base makes the cycle productive  
PASS  one qF proof  1 proof(s)
PASS  two b-occurrences have distinct paths  $[0] vs $[1]
PASS  four leaf occurrences are path-distinguishable  
PASS  child state retracted  
PASS  parent state retracted transitively  
PASS  max_rounds=0 is a defined failure  
PASS  cooperative cancellation aborts evaluation  

ALL COUNTEREXAMPLES PASS
```
