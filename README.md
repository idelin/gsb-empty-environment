# Mini Autograd

A minimal reverse-mode automatic-differentiation engine implemented from
scratch in **Python 3.11 standard library only** (no NumPy, no autodiff
framework). Tensors are small CPU buffers (`list[float]` + shape); all
forward kernels and backward rules are hand written.

## Running

```bash
python3.11 -m unittest discover -s tests -v   # 33 tests
python3.11 demo.py                            # annotated walkthrough
```

Captured output for both commands is in [SAMPLES.md](SAMPLES.md).

## Package layout

| File | Responsibility |
| --- | --- |
| `mini_autograd/storage.py` | Ref-counted flat `Storage` buffers ("heap memory") |
| `mini_autograd/tensor.py` | `Tensor`, version counters, in-place mutation, commits |
| `mini_autograd/kernels.py` | Pure forward math (add/mul/matmul/relu/exp/log/dropout/...) |
| `mini_autograd/graph.py` | `Graph`, `Node`, `no_grad`, thread-local graph stack |
| `mini_autograd/ops.py` | Differentiable operators: forward + local backward rules |
| `mini_autograd/engine.py` | Topological backward with atomic staging transactions |
| `mini_autograd/checkpoint.py` | Recompute nodes (input-version + random-key binding) |
| `mini_autograd/rng.py` | Deterministic, key-seeded, thread-local random draws |
| `mini_autograd/numeric.py` | Independent central-difference gradient checker |
| `mini_autograd/errors.py` | Exception hierarchy |

## Quick start

```python
from mini_autograd import Graph, tensor, mul, relu, sum

with Graph().use():
    x = tensor([1.0, -2.0, 3.0], requires_grad=True)
    loss = sum(mul(relu(x), relu(x)))
    loss.backward()
print(x.grad.data)          # [2.0, 0.0, 6.0]
```

## How each requirement is met

- **Reverse pass in topological order** — `engine._run_engine` performs a DFS
  over node producers and executes nodes in reversed post-order, aggregating
  each output's gradient (`accumulate`) before invoking the node. A shared node
  is visited once and its `run_count` stays `1` even with multiple consumers
  (`tests/test_ops.py::test_shared_node_run_count_is_one`).
- **Retained vs released graphs** — `backward(retain_graph=True)` keeps nodes
  and accumulates leaf gradients on repeated calls; the default call invokes
  `Graph.release()`, which drops every forward reference, and a second
  `backward` raises `GraphReleasedError`.
- **In-place modification with version counters** — leaves carry a monotonically
  increasing `version`; nodes snapshot `saved_versions` at forward time and
  re-check them lazily when the node runs. Mutating a saved input after forward
  therefore raises `VersionMismatchError` during backward. In-place writes are
  only legal on leaves (`InplaceError` otherwise).
- **Atomic gradient transactions** — backward accumulates into a private
  `staged` dict. Commits to `Tensor.grad` happen only after *all* nodes
  succeed; any error (injected operator exception, stale version, or a
  `cancel(node)` hook raising `BackwardAbortedError`) discards the staging
  buffer, so previously committed gradients are untouched. Repeated backwards,
  shared parameters, retained graphs and cancellation all use the same
  transaction boundary.
- **Independent graphs do not pollute each other** — every `Graph` has its own
  node list and `RLock`; failure in one transaction cannot touch another graph's
  leaves. Leaf commits additionally take a global commit lock for shared
  parameters driven from multiple threads.
- **Recompute / checkpoint nodes** — `checkpoint(fn, *args, seed=...)` runs the
  opaque forward under `no_grad()` and a bound `rng_key`, saving only the
  tracked inputs (with version snapshots) and the key. Backward replays the
  segment on a private throwaway graph under the same key (so dropout masks
  reproduce exactly), differentiates it as a nested non-committing transaction,
  and feeds input gradients back into the outer graph.
- **Storage lifecycle** — a `Storage` is recycled only when its refcount drops
  to zero. Tensors and explicit aliases (`share_storage()`) hold references, so
  releasing one graph never frees a buffer still referenced by another graph or
  a live tensor.
- **Independent numerical verification** — `numeric.gradcheck` perturbs every
  leaf coordinate with a central difference (sharing only forward kernels with
  the analytical engine) and asserts the relative error against `backward`.
  Elementwise, reductions, `matmul`, `exp`/`log`/`pow`, and checkpointed
  segments are all grad-checked (errors ~`1e-10`–`1e-12`).
- **Fault injection and concurrent lifecycle checks** — the `faulty` operator is
  an identity in forward and raises `InjectedBackwardError` in backward under a
  thread-local `faulty_backward(token)` context. Threaded tests drive multiple
  independent graphs backwards concurrently and interleave failures/releases.

## Error taxonomy

`AutogradError` is the base class: `GraphReleasedError`,
`VersionMismatchError`, `BackwardAbortedError`, `InjectedBackwardError`,
`InplaceError`, `InvalidBackwardError`, `StorageFreedError`.

## Scope notes

- Broadcasting supports scalars and equal-shape tensors only.
- `matmul` supports 2-D tensors; reductions produce scalar tensors.
- In-place mutation is intentionally restricted to leaves (PyTorch-like).
