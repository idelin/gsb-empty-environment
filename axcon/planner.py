"""Contraction-order planner.

Enumerates binary contraction trees over the input tensors and picks the one
minimising the summed estimate of intermediate non-zeros.  Estimates only use
dimension arithmetic (products of axis sizes) -- the Cartesian product of the
data itself is never expanded.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Operand:
    """A tensor (input or intermediate) as seen by the planner."""

    name: str
    labels: tuple
    dims: dict
    nnz: int

    @classmethod
    def from_tensor(cls, tensor):
        return cls(tensor.name, tuple(tensor.axes), dict(tensor.shape), tensor.nnz)


@dataclass
class StepSpec:
    left: str
    right: str
    out_name: str
    out_axes: tuple
    est_nnz: int


@dataclass
class Plan:
    steps: list
    total_est_nnz: int
    orders_considered: int

    def describe(self):
        lines = []
        for i, step in enumerate(self.steps):
            lines.append(
                f"step {i}: {step.left} * {step.right} -> {step.out_name}"
                f"[{','.join(step.out_axes)}] est_nnz={step.est_nnz}")
        lines.append(f"total estimated intermediate nnz: {self.total_est_nnz}")
        return "\n".join(lines)


def _estimate_pair(x, y, kept_labels):
    bound = 1
    dims = dict(x.dims)
    dims.update(y.dims)
    for label in kept_labels:
        bound *= dims[label]
    return min(x.nnz * y.nnz, bound)


def plan_order(operands, out_axes):
    """Return the minimum-estimated-intermediate-nnz Plan for ``operands``."""
    operands = tuple(operands)
    out_set = set(out_axes)
    memo = {}
    considered = [0]

    def rec(ops):
        if len(ops) == 1:
            return 0, ()
        key = tuple(sorted(o.name for o in ops))
        if key in memo:
            return memo[key]
        best = None
        for i in range(len(ops)):
            for j in range(i + 1, len(ops)):
                x, y = ops[i], ops[j]
                rest = tuple(o for k, o in enumerate(ops) if k not in (i, j))
                rest_labels = set()
                for o in rest:
                    rest_labels.update(o.labels)
                kept = []
                for label in x.labels + y.labels:
                    if label not in kept and (label in rest_labels or label in out_set):
                        kept.append(label)
                est = _estimate_pair(x, y, kept)
                dims = dict(x.dims)
                dims.update(y.dims)
                new_op = Operand(
                    name=f"({x.name}*{y.name})",
                    labels=tuple(kept),
                    dims={l: dims[l] for l in kept},
                    nnz=est,
                )
                sub_cost, sub_steps = rec(rest + (new_op,))
                considered[0] += 1
                total = est + sub_cost
                step = StepSpec(x.name, y.name, new_op.name, tuple(kept), est)
                if best is None or total < best[0]:
                    best = (total, (step,) + sub_steps)
        memo[key] = best
        return best

    cost, steps = rec(operands)
    return Plan(steps=list(steps), total_est_nnz=cost,
                orders_considered=max(1, considered[0]))
