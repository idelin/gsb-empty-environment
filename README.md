# Checkpointing / Rematerialisation Toolkit

Pure Python 3.11, standard library only (`unittest` for tests).  No
autograd, no tensor libraries: the small dense-tensor implementation in
`checkpoint/tensor.py` is self-contained and verified independently by
finite differences.

## What it does

* **Forward:** stores only the planned checkpoint activations; everything
  else is freed as soon as its last consumer has run.
* **Backward:** recomputes missing activations from the nearest stored
  ancestors ("selective save, recompute the rest").
* **Deterministic RNG replay:** the RNG state is snapshotted before every
  node; a recompute restores the snapshot of the segment's first node, so
  stochastic ops (e.g. Gaussian noise injection) replay **bit-exactly**.
  Every recomputed tensor is audited against the forward pass.
* **Statistics:** peak memory (including temporary gradient buffers),
  recompute count and recompute cost are reported per run.
* **Chain graphs:** exact DP gives the minimum-recompute plan under a
  memory budget; optimality on small instances is proven by exhaustive
  search (`plan_chain_bruteforce`).
* **General DAGs:** a greedy heuristic plus a computable lower bound.
* **Safety:** side-effecting nodes cannot be recomputed unless explicitly
  declared `replayable`; shared views are detected by storage identity
  and every read checks the storage version.
* **Independent validation:** an event simulator replays the
  alloc/free/read stream and proves the executed plan never reads a
  released activation.

## Layout

```
checkpoint/
  tensor.py          minimal tensors, ops, views with version checking
  rng.py             Rng: snapshot/restore wrapper over random.Random
  graph.py           Node, topo sort, descendant counts
  planner.py         chain DP + brute force, DAG greedy + lower bound
  engine.py          checkpointed forward / recomputing backward
  simulator.py       independent alloc/free/read event checker
  standard_nodes.py  layer/noise/loss node factories, chain builder
tests/               unittest suite (27 tests)
examples/demo.py     runnable end-to-end demo
```

## Cost model (chain)

Positions `0..n` (`x_0` = input, `x_i` = output of node `i`, cost `c_i`,
size `s_i`).  Checkpoints persist through backward.  The backward step of
node `j` needs `x_{j-1}` and `x_j`; missing activations are recomputed
from the nearest earlier checkpoint and freed at the end of the step.
For a segment between consecutive checkpoints `i < k` (`k = n+1` means
the chain end):

```
cost(i, k)   = sum_{s=i+1}^{k-1} c_s * (k - s + 1)      (k <= n)
cost(i, n+1) = sum_{s=i+1}^{n}   c_s * (n - s + 1)
peak memory  = sum(checkpoint sizes) + 2 * max(s_i) + workspace
```

`workspace` (see `gradient_workspace`) covers input activations, all
accumulated input gradients, the live output gradient and the transient
old/new/temporary buffers during gradient accumulation — so the peak
**includes temporary gradients**.

## Optimality of the chain DP

`dp(i, m)` = min recompute cost of the suffix given `x_i` is stored and
`m` checkpoint bytes remain:

```
dp(i, m) = min over k in (i, n+1], size_k <= m of
               cost(i, k) + dp(k, m - size_k)
```

A segment's cost depends only on its endpoints, so subproblems are
independent (optimal substructure) and the recurrence enumerates every
feasible first move — hence the global minimum.  The test suite
additionally compares the DP against exhaustive subset enumeration on 60+
random small chains (including forced-checkpoint cases); they always
agree, and the engine's measured recompute cost equals the planned cost
exactly.

## DAG heuristic and lower bound

Greedy: store nodes by descending `cost * (1 + descendants) / size`
while the budget allows.  Lower bound: every unstored node is recomputed
at least once, so `LB = total_cost - max knapsack(storable cost)`; the
demo and tests report `LB` alongside the measured heuristic cost.

## Side effects and views

* `Node(side_effect=True)` raises `RecomputeError` if a recompute would
  re-execute it, unless declared `replayable=True`.  Its output should be
  a required checkpoint (`required=` in the planners).
* `Tensor.view()` shares storage; `Storage.version` bumps on in-place
  mutation; reading a stale view raises `VersionError`.  The engine
  records view aliases (identity check) and charges them 0 bytes.

## Run

```
python3.11 examples/demo.py
python3.11 -m unittest discover -s tests -v
```

## Sample output

```
Chain: optimal checkpoint plan under a memory budget
memory budget: 13037 bytes (workspace 12544, frontier reserve 256)
optimal checkpoints: ['x'] + ['L2', 'loss']
planned recompute cost: 38.0 (brute-force optimum: 38.0)
  peak memory:      8712 bytes (budget 13037)
  recompute count:  14 node recomputations
  recompute cost:   38.0 (matches plan: True)
gradients identical to full-save mode: True
stochastic recompute bit-exact (audit): True
independent event simulation: 27 reads, 0 violations, peak 8712 matches: True

DAG: greedy heuristic with a reported lower bound
greedy checkpoints: ['A', 'C', 'loss']
measured recompute cost: 14.0   lower bound: 5.0
peak memory: 1752 bytes (budget 2424)
gradients identical to full-save mode: True
```

## Test results

```
$ python3.11 -m unittest discover -s tests
...........................
Ran 27 tests in ~2s
OK
```

Coverage: tensor ops and views, DP==brute-force optimality, required
checkpoints, infeasible budgets, gradient parity with full-save mode,
bit-exact stochastic replay, finite-difference gradient checks
(independent numerical verification), side-effect recompute guards,
view identity/version checks, DAG heuristic vs lower bound, and event
simulation (read-after-free detection).
