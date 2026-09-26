"""Offline loudness normalization planner (pure standard library).

Public API:

* :func:`plan_normalization` / :class:`SegmentSpec` — build a plan.
* :class:`Plan` — serializable plan (JSON round-trip, digest, latency,
  tolerances, conflicts with residuals).
* :func:`apply_plan` / :func:`verify_plan_input` — chunked rendering with
  input-digest verification.
* :func:`write_output` — atomic file rendering with cancellation.
* :mod:`loudnorm.fixtures` — local sample arrays and sample rate.
"""

from .apply import PlanMismatchError, apply_plan, verify_plan_input
from .audio import input_digest, region_peak, region_rms
from .constants import (
    GAIN_TOLERANCE_DB,
    PEAK_TOLERANCE,
    PLAN_FORMAT_VERSION,
    SILENCE_RMS_THRESHOLD,
    processing_latency_samples,
)
from .curve import CurveSegment, GainCurve
from .plan import STATUS_NORMALIZED, STATUS_PARTIAL, Plan
from .planner import SegmentSpec, plan_normalization
from .writer import WriteCancelled, write_output

__all__ = [
    "GAIN_TOLERANCE_DB",
    "PEAK_TOLERANCE",
    "PLAN_FORMAT_VERSION",
    "SILENCE_RMS_THRESHOLD",
    "STATUS_NORMALIZED",
    "STATUS_PARTIAL",
    "CurveSegment",
    "GainCurve",
    "Plan",
    "PlanMismatchError",
    "SegmentSpec",
    "WriteCancelled",
    "apply_plan",
    "input_digest",
    "plan_normalization",
    "processing_latency_samples",
    "region_peak",
    "region_rms",
    "verify_plan_input",
    "write_output",
]
