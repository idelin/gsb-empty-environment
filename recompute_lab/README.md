# recompute_lab

Selective-activation checkpointing ("save some nodes in forward, recompute the
rest in backward") implemented from scratch in pure Python 3.11 — standard
library and `unittest` only.  No autograd framework, no tensor library.

## Layout

| file | role |
|---|---|
| `minitensor.py` | tiny CPU tensor (flat list + shape/strides), views share a `Storage` with a mutation `version` counter |
| `chain_dp.py`   | exact DP for the min-recompute-cost checkpoint plan on a chain under a memory budget, plus a brute-force enumerator |
| `dag_plan.py`   | greedy heuristic for general DAGs + a knapsack-relaxation lower bound |
| `simulator.py`  | independent event-level simulation of a plan; validates no read-after-free; peak includes temporary gradients |
| `engine.py`     | real forward/backward execution with checkpointing, deterministic RNG replay, side-effect guard, identity/version checks |
| `numerical.py`  | independent finite-difference gradient verification |
| `examples/run_sample.py` | end-to-end demo |
| `tests/test_recompute.py` | 18 unit tests |

## Model

Nodes `0..n`, node `i` has activation size `a[i]` and recompute cost `c[i]`.
A plan is a checkpoint set `C` (node 0, the input, is always kept).  During
backward, a missing activation `j` is recomputed forward from the nearest
checkpoint below it, materialising the segment transiently.

- persistent memory = `sum a[k]` for `k in C`
- transient memory  = max segment sum between consecutive checkpoints
- peak = persistent + transient ≤ budget `B`
- cost = `sum c[t]` over non-checkpoints (each recomputed at most once)

### Chain: exact DP and small-scale optimality proof

`solve_chain` sweeps checkpoints left→right keeping a Pareto frontier of
`(persistent, transient, cost)` states per last checkpoint; transitions append
one segment and prune dominated states.  Every plan corresponds to exactly one
DP path and all updates are monotone, so the DP attains the optimum over all
feasible plans (full argument in the module docstring).  This is *proven
empirically on small scales* two ways in the tests:

- `test_exhaustive_small_domain_proof`: **every** instance with `n ≤ 3`,
  `a, c ∈ {1,2,3}`, and every budget — DP cost equals brute-force `2^n`
  enumeration (thousands of instances, exhaustive);
- `test_matches_brute_force_random` / `test_dp_never_worse_than_any_subset`:
  150+ random instances up to `n = 9`, DP equals brute force and dominates
  every feasible subset.

### DAG: heuristic + lower bound

`greedy_dag_plan` first adds checkpoints that reduce the simulated peak until
the plan is feasible (adding checkpoints *shrinks* recompute cones), then adds
the best cost-saving checkpoints while slack remains.  Feasibility and cost
come from the event-level simulator.  `lower_bound_cost` relaxes the problem
(each node recomputed at most once, transient memory free) to a 0/1 knapsack
solved exactly by DP; the true optimum can only be worse, so
`total_cost − knapsack(B)` is a valid lower bound.  The sample prints the
bracket `[lower_bound, heuristic_cost]`; tests assert
`heuristic ≥ lower_bound` on 60 random DAGs.

## Execution guarantees (engine)

- **Deterministic random replay** — each random op (`dropout`) draws from
  `random.Random(f"{seed}:{node}")` recreated at every (re)computation, so a
  recomputed random op reproduces the original forward bit-for-bit; gradients
  from a checkpointed run are *exactly* equal to full-save mode
  (`test_checkpoint_grads_match_full_save_exactly`).
- **Side-effect guard** — a node with `side_effect=True` raises
  `SideEffectRecomputeError` if a plan would recompute it, unless explicitly
  declared `allow_recompute=True`.
- **Identity & version checks** — stored activations record
  `(id, storage_id, version)`; backward reads re-validate and raise
  `StaleActivationError` on in-place mutation, including mutation through a
  shared-storage view (`reshape`).
- **Peak includes temporaries** — gradient buffers allocated during backward
  are tracked by the same `MemoryTracker` and appear in the event trace.
- **Independent event simulation** — `simulator.verify_trace` replays the
  engine's emitted trace and rejects any read of a freed activation; the test
  suite feeds it both the real engine trace (must pass, peak must match) and
  hand-crafted bad traces (must raise `ReadAfterFreeError`).

## Independent numerical verification

`numerical.finite_difference_grad` computes central differences using only
forward evaluations (no backward pass), an independent check of the analytic
gradients; max abs error ≈ 1e-11 on the sample network.

## Run

```bash
python3 examples/run_sample.py     # demo
python3 -m unittest discover -s tests -v   # tests
```

## Sample output

```
================================================================
1. Chain: optimal checkpoint plan under a memory budget
================================================================
sizes=[2, 4, 3, 5, 2, 6, 3, 4]
costs=[0, 5, 2, 8, 3, 7, 4, 6]
budget=18
checkpoints      : [0, 3, 6]
recompute cost   : 23.0  (brute force agrees: 23.0)
peak memory      : 18.0 <= 18

================================================================
2. DAG: greedy heuristic with a reported lower bound
================================================================
parents={0: [], 1: [0], 2: [1], 3: [2, 1], 4: [3], 5: [4, 2], 6: [5]}
sizes={0: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 1} costs={0: 0, 1: 4, 2: 2, 3: 7, 4: 3, 5: 5, 6: 1} budget=6
checkpoints      : [0, 2, 3, 5]
heuristic cost   : 7.0
lower bound      : 1.0  (optimum lies in [1.0, 7.0])
peak memory      : 6 <= 6

================================================================
3. Engine: checkpointed backward == full-save backward (exact)
================================================================
full-save peak   : 100 elements
checkpoint peak  : 76 elements
recomputes       : {'nl0': 1, 'dr0': 1, 'nl1': 1, 'dr1': 1, 'nl2': 1, 'dr2': 1, 'rv': 1}
outputs identical: True
grad x identical : True
grad w1 identical: True

================================================================
4. Independent event simulation of the same plan
================================================================
simulator: feasible=True peak=76 recompute_counts={'nl0': 1, 'dr0': 1, 'nl1': 1, 'dr1': 1, 'nl2': 1, 'dr2': 1, 'rv': 1}
engine trace re-validated by verify_trace: peak=76 (== engine stats 76)

================================================================
5. Independent numerical verification (finite differences)
================================================================
analytic grad x  : [0.062678, 0.098536, 0.120485, 0.293565, 0.422037, 0.38866]
finite-difference: [0.062678, 0.098536, 0.120485, 0.293565, 0.422037, 0.38866]
max abs error    : 9.593e-11
```

## Test results

```
$ python3 -m unittest discover -s tests
..................
----------------------------------------------------------------------
Ran 18 tests in 1.763s

OK
```

Coverage: chain DP == brute force (exhaustive small domain + random), DAG
heuristic feasibility and `heuristic ≥ lower_bound`, gradient equality
full-save vs checkpointed (bitwise), finite-difference verification,
deterministic dropout replay, side-effect guard (declared/undeclared),
view identity/version checks, read-after-free detection, engine-trace
re-validation, peak accounting including temporary gradients.
