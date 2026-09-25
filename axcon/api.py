"""High-level API: plan a multi-input contraction and execute it."""

from __future__ import annotations

import os
import re
from dataclasses import dataclass, field

from .cache import ContractionCache
from .dense import validate_inputs
from .engine import Budget, StepStats, contract_pair
from .errors import BudgetExceeded, ValidationError
from .planner import Operand, Plan, plan_order


@dataclass
class PlanStats:
    est_intermediate_nnz: int = 0
    actual_intermediate_nnz: int = 0
    orders_considered: int = 0
    chunks_processed: int = 0
    chunks_skipped: int = 0
    contributions: int = 0
    spill_files: int = 0
    spilled_entries: int = 0
    cache_hits: int = 0
    resumed_steps: int = 0
    output_nnz: int = 0


@dataclass
class PlanResult:
    tensor: object
    axis_mapping: dict
    plan: Plan
    stats: PlanStats
    complete: bool
    error: str = None


def _sanitize(name):
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", name)


def contract_many(inputs, out_axes, *, budget=None, cache=None, workdir=None,
                  chunk_hook=None, plan=None):
    """Plan and run the contraction of ``inputs`` onto ``out_axes``.

    Returns a ``PlanResult`` with the result tensor, the output axis mapping
    (label -> position) and plan statistics.  If the budget aborts a step the
    result is returned with ``complete=False`` and no tensor.
    """
    inputs = list(inputs)
    out_axes = tuple(out_axes)
    validate_inputs(inputs, out_axes)
    if plan is None:
        plan = plan_order([Operand.from_tensor(t) for t in inputs], out_axes)
    if plan.steps:
        final = plan.steps[-1]
        if set(final.out_axes) != set(out_axes):
            raise ValidationError(
                f"final step produces {final.out_axes!r}, expected {out_axes!r}")
        final.out_axes = out_axes  # emit the final result in requested axis order

    stats = PlanStats(est_intermediate_nnz=plan.total_est_nnz,
                      orders_considered=plan.orders_considered)
    env = {t.name: t for t in inputs}
    # Lineage stamps: inputs are stamped by (name, version); intermediates by
    # the key of the step that produced them, so a delta anywhere upstream
    # changes every downstream key and invalidates the cache structurally.
    stamps = {t.name: ("tensor", t.name, t.version) for t in inputs}
    try:
        for step in plan.steps:
            left, right = env[step.left], env[step.right]
            key = ("contract", stamps[step.left], stamps[step.right],
                   step.out_axes)
            cached = cache.get(key) if cache is not None else None
            if cached is not None:
                stats.cache_hits += 1
                env[step.out_name] = cached
                stamps[step.out_name] = key
                continue
            step_dir = (os.path.join(workdir, _sanitize(step.out_name))
                        if workdir else None)
            tensor, step_stats = contract_pair(
                left, right, step.out_axes, budget=budget,
                workdir=step_dir, chunk_hook=chunk_hook, out_name=step.out_name)
            _accumulate(stats, step_stats, final=step is plan.steps[-1])
            if cache is not None:
                cache.put(key, tensor)
            env[step.out_name] = tensor
            stamps[step.out_name] = key
        result = env[plan.steps[-1].out_name] if plan.steps else inputs[0]
        stats.output_nnz = result.nnz
        axis_mapping = {label: i for i, label in enumerate(result.axes)}
        return PlanResult(tensor=result, axis_mapping=axis_mapping, plan=plan,
                          stats=stats, complete=True)
    except BudgetExceeded as exc:
        if exc.stats is not None:
            _accumulate(stats, exc.stats, final=False)
        return PlanResult(tensor=None, axis_mapping={}, plan=plan, stats=stats,
                          complete=False, error=str(exc))


def _accumulate(stats, step_stats, *, final):
    stats.chunks_processed += step_stats.chunks_processed
    stats.chunks_skipped += step_stats.chunks_skipped
    stats.contributions += step_stats.contributions
    stats.spill_files += step_stats.spill_files
    stats.spilled_entries += step_stats.spilled_entries
    stats.resumed_steps += 1 if step_stats.resumed else 0
    if not final:
        stats.actual_intermediate_nnz += step_stats.output_nnz
