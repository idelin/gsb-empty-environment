"""Applying a plan to samples, whole-block or in arbitrary chunks.

``gain_at`` depends only on the sample index, so chunked application is
bit-identical to whole-block application for any chunking of the input.
"""

from __future__ import annotations

from typing import List, Optional, Sequence

from .audio import input_digest
from .plan import Plan


class PlanMismatchError(ValueError):
    """Raised when a plan is applied to input it was not built from."""


def verify_plan_input(plan: Plan, samples: Sequence[float],
                      sample_rate: int) -> None:
    """Bind a plan to its source: rate, length, and content digest."""
    if sample_rate != plan.sample_rate:
        raise PlanMismatchError(
            "sample rate %d does not match plan (%d)"
            % (sample_rate, plan.sample_rate))
    if len(samples) != plan.num_samples:
        raise PlanMismatchError(
            "input has %d samples, plan was built for %d"
            % (len(samples), plan.num_samples))
    if input_digest(samples, sample_rate) != plan.input_digest:
        raise PlanMismatchError(
            "input digest mismatch: the source audio changed since this "
            "plan was created; the plan is invalid and must be rebuilt")


def apply_plan(
    samples: Sequence[float],
    sample_rate: int,
    plan: Plan,
    chunk_size: Optional[int] = None,
    *,
    verify: bool = True,
) -> List[float]:
    """Render ``samples`` through ``plan``.

    ``chunk_size`` only controls internal iteration; the result is exactly
    the same for any chunk size, including ``None`` (single block).
    """
    if verify:
        verify_plan_input(plan, samples, sample_rate)
    if chunk_size is not None and chunk_size <= 0:
        raise ValueError("chunk_size must be positive or None")
    curve = plan.gain_curve()
    total = len(samples)
    out = [0.0] * total
    step = chunk_size if chunk_size else total or 1
    for lo in range(0, total, step):
        hi = min(lo + step, total)
        for n in range(lo, hi):
            out[n] = samples[n] * curve.gain_at(n)
    return out
