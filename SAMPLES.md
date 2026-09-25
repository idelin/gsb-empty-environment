# Run Samples

Captured with `Python 3.11` (standard library only).

## Tests

Command:

```
python3.11 -m unittest discover -s tests -v
```

Result:

```
test_checkpoint_numeric (test_checkpoint.TestCheckpoint.test_checkpoint_numeric) ... ok
test_checkpoint_saves_no_inner_nodes (test_checkpoint.TestCheckpoint.test_checkpoint_saves_no_inner_nodes) ... ok
test_mutated_input_aborts_recompute (test_checkpoint.TestCheckpoint.test_mutated_input_aborts_recompute) ... ok
test_random_key_replays_dropout (test_checkpoint.TestCheckpoint.test_random_key_replays_dropout) ... ok
test_concurrent_release_and_backward_is_serialized (test_concurrency.TestConcurrentBackward.test_concurrent_release_and_backward_is_serialized) ... ok
test_independent_graphs_backward_in_parallel (test_concurrency.TestConcurrentBackward.test_independent_graphs_backward_in_parallel) ... ok
test_cancel_aborts_whole_transaction (test_lifecycle.TestAtomicCancel.test_cancel_aborts_whole_transaction) ... ok
test_injected_failure_is_atomic (test_lifecycle.TestAtomicCancel.test_injected_failure_is_atomic) ... ok
test_error_does_not_poison_sibling_graph (test_lifecycle.TestIndependentGraphs.test_error_does_not_poison_sibling_graph) ... ok
test_repeat_backward_accumulates (test_lifecycle.TestRetainGraph.test_repeat_backward_accumulates) ... ok
test_retain_then_release (test_lifecycle.TestRetainGraph.test_retain_then_release) ... ok
test_second_backward_without_retain_rejected (test_lifecycle.TestRetainGraph.test_second_backward_without_retain_rejected) ... ok
test_failure_in_one_graph_preserves_committed_grad (test_lifecycle.TestSharedParameters.test_failure_in_one_graph_preserves_committed_grad) ... ok
test_two_graphs_sharing_leaf (test_lifecycle.TestSharedParameters.test_two_graphs_sharing_leaf) ... ok
test_copy_data_bumps_version (test_lifecycle.TestVersionCounting.test_copy_data_bumps_version) ... ok
test_graph_is_usable_again_after_failure (test_lifecycle.TestVersionCounting.test_graph_is_usable_again_after_failure) ... ok
test_inplace_after_forward_rejected_at_backward (test_lifecycle.TestVersionCounting.test_inplace_after_forward_rejected_at_backward) ... ok
test_inplace_on_nonleaf_forbidden (test_lifecycle.TestVersionCounting.test_inplace_on_nonleaf_forbidden) ... ok
test_successful_retry_after_atomic_failure (test_lifecycle.TestVersionCounting.test_successful_retry_after_atomic_failure) ... ok
test_failed_replay_pollutes_nothing (test_misc.TestCheckpointFailure.test_failed_replay_pollutes_nothing) ... ok
test_backward_multi_one_transaction (test_misc.TestMultiRoot.test_backward_multi_one_transaction) ... ok
test_no_grad_suspends_recording (test_misc.TestNoGrad.test_no_grad_suspends_recording) ... ok
test_elementwise_and_reductions_numeric (test_ops.TestFiniteGraphs.test_elementwise_and_reductions_numeric) ... ok
test_matmul_numeric (test_ops.TestFiniteGraphs.test_matmul_numeric) ... ok
test_matmul_shapes_and_values (test_ops.TestFiniteGraphs.test_matmul_shapes_and_values) ... ok
test_pow_div_sub_neg_numeric (test_ops.TestFiniteGraphs.test_pow_div_sub_neg_numeric) ... ok
test_non_scalar_requires_explicit_gradient (test_ops.TestRootValidation.test_non_scalar_requires_explicit_gradient) ... ok
test_diamond_dependency_deduplicated (test_ops.TestSharedNodes.test_diamond_dependency_deduplicated) ... ok
test_shared_node_run_count_is_one (test_ops.TestSharedNodes.test_shared_node_run_count_is_one) ... ok
test_shared_node_runs_backward_once (test_ops.TestSharedNodes.test_shared_node_runs_backward_once) ... ok
test_alias_keeps_storage_alive (test_storage.TestStorageRefcount.test_alias_keeps_storage_alive) ... ok
test_graph_release_does_not_free_shared_storage (test_storage.TestStorageRefcount.test_graph_release_does_not_free_shared_storage) ... ok
test_release_recycles_when_unreferenced (test_storage.TestStorageRefcount.test_release_recycles_when_unreferenced) ... ok
test_use_after_free_is_detected (test_storage.TestStorageRefcount.test_use_after_free_is_detected) ... ok

----------------------------------------------------------------------
Ran 34 tests in 0.211s

OK
```

## Demo

Command:

```
python3.11 demo.py
```

Result:

```text

====================================================================
1. Reverse-topological accumulation; a shared node runs once
====================================================================
loss = 18.0  grad = [3.0, 3.0, 3.0]
relu node executed 1 time(s) despite 2 consumers

====================================================================
2. In-place version counting detects a stale saved input
====================================================================
version before mutation: 0
version after mutation:  1
backward rejected: tensor saved by node 'mul' was modified in place after forward (version 0 -> 1); refusing to run backward on stale data
x.grad stays untouched: None

====================================================================
3. Retained vs released graphs
====================================================================
grad after backward #1: [2.0, 4.0]
grad after backward #2 (accumulates): [4.0, 8.0]
backward #3 rejected: graph was released by a previous backward(retain_graph=False); rebuild the forward pass or pass retain_graph=True

====================================================================
4. Atomic transaction; injected failure cannot poison gradients
====================================================================
committed grad from healthy graph: [2.0, 2.0]
broken graph raised: injected failure in backward of 'boom'
grad after failed transaction: [2.0, 2.0] (unchanged)
cancel raised: backward cancelled at node 'sum'
grad after cancel: None

====================================================================
5. Checkpoint: bind input versions + random key, replay in backward
====================================================================
checkpoint+dropout grad: [0.0, 0.0, 1.6666666666666667, 1.6666666666666667, 0.0]
central-difference gradcheck relative error: 3.898e-12

====================================================================
6. Reference-counted storage survives while any graph needs it
====================================================================
storage refs held by tensor + alias (graphs hold the tensors): 2
g1 released; storage alive? True  data: [2.0, -1.0, 0.5]
grad after g2: [4.333333333333333, -2.0, 1.3333333333333333]
tensor gone; alias still reads storage 104 : [2.0, -1.0, 0.5]
alias gone -> buffer recycled (no exception in demo teardown)

====================================================================
7. Independent finite differences over a matmul network
====================================================================
analytic=[[1.9388086967934481, -3.446234376017649, 8.377666235607855, 1.0163243096769554], [1.5, 1.5, -0.7, -0.7]]
numeric =[[1.9388086968774585, -3.4462343765362387, 8.377666237002757, 1.0163243097771613], [1.500000000032031, 1.500000000032031, -0.6999999999202088, -0.6999999999202088]]
rel_err =1.665e-10
matmul+exp relative error: 1.665e-10

All demos completed.
```
