"""Contraction-order planner over explicit axis labels.

Enumerates every pairwise contraction order and picks the one with the
smallest total estimated intermediate nonzeros. Sizes and indices are
always explicit; nothing dense is ever materialized here.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .errors import ValidationError


@dataclass(frozen=True)
class OperandRef:
    kind: str  # "input" or "step"
    index: int


@dataclass
class PlanStep:
    left: OperandRef
    right: OperandRef
    contracted: tuple
    out_labels: tuple
    out_shape: tuple
    est_nnz: float
    actual_nnz: object = None
    chunks: int = 0
    spilled_bytes: int = 0


@dataclass(frozen=True)
class AxisMapping:
    """Maps each output label to its axis position and size."""
    labels: tuple
    shape: tuple

    def index_of(self, label):
        return self.labels.index(label)

    def size_of(self, label):
        return self.shape[self.labels.index(label)]


@dataclass
class Plan:
    output: AxisMapping
    steps: list
    est_intermediate_total: float
    stats: dict = field(default_factory=dict)


def collect_sizes(tensors):
    """Validate and merge axis sizes across all inputs."""
    sizes = {}
    for tensor in tensors:
        for label, dim in zip(tensor.labels, tensor.shape):
            if label in sizes and sizes[label] != dim:
                raise ValidationError(
                    f"axis {label!r} has inconsistent sizes "
                    f"{sizes[label]} and {dim}")
            sizes[label] = dim
    return sizes


def estimate_join(nnz_a, labels_a, nnz_b, labels_b, sizes, out_labels):
    """Expected result nonzeros of a hash join under uniform density."""
    if nnz_a == 0 or nnz_b == 0:
        return 0.0
    denom = 1
    for label in labels_a & labels_b:
        denom *= max(sizes[label], 1)
    est = nnz_a * nnz_b / denom
    capacity = 1
    for label in out_labels:
        capacity *= max(sizes[label], 1)
    return min(est, float(capacity))


def _validate_output(output_labels, sizes):
    if len(set(output_labels)) != len(output_labels):
        raise ValidationError(f"duplicate output labels {output_labels!r}")
    for label in output_labels:
        if label not in sizes:
            raise ValidationError(
                f"output label {label!r} is not bound by any input")


def enumerate_plans(tensors, output_labels):
    """Return all pairwise contraction plans, cheapest estimate first."""
    output_labels = tuple(output_labels)
    sizes = collect_sizes(tensors)
    _validate_output(output_labels, sizes)
    output = AxisMapping(output_labels, tuple(sizes[l] for l in output_labels))
    if len(tensors) == 1:
        return [Plan(output=output, steps=[], est_intermediate_total=0.0)]
    plans = []

    def rec(operands, steps, cost):
        if len(operands) == 1:
            plans.append(Plan(
                output=output, steps=list(steps), est_intermediate_total=cost,
                stats={"est_max_step": max((s.est_nnz for s in steps),
                                           default=0.0)}))
            return
        for i in range(len(operands)):
            for j in range(i + 1, len(operands)):
                a, b = operands[i], operands[j]
                rest = [o for k, o in enumerate(operands) if k not in (i, j)]
                needed = set(output_labels)
                for other in rest:
                    needed |= other["labels"]
                union = a["labels"] | b["labels"]
                out_labels = tuple(sorted(union & needed))
                est = estimate_join(a["nnz"], a["labels"], b["nnz"],
                                    b["labels"], sizes, out_labels)
                step = PlanStep(
                    left=OperandRef(*a["ref"]), right=OperandRef(*b["ref"]),
                    contracted=tuple(sorted(union - needed)),
                    out_labels=out_labels,
                    out_shape=tuple(sizes[l] for l in out_labels),
                    est_nnz=est)
                merged = {"ref": ("step", len(steps)),
                          "labels": set(out_labels), "nnz": est}
                rec(rest + [merged], steps + [step], cost + est)

    operands = [{"ref": ("input", k), "labels": set(t.labels),
                 "nnz": float(t.nnz)} for k, t in enumerate(tensors)]
    rec(operands, [], 0.0)
    plans.sort(key=lambda p: p.est_intermediate_total)
    return plans


def build_plan(tensors, output_labels):
    """Return the cheapest estimated plan."""
    return enumerate_plans(tensors, output_labels)[0]
