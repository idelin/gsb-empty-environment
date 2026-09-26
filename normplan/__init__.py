"""Offline volume-normalization planner (Python 3.11+, stdlib only).

Declared latency and tolerances:
  * PLANNER_LATENCY: offline, requires the complete input signal.
  * APPLY_LATENCY_SAMPLES: 0 -- gain is a pure function of sample index.
  * RMS_TOLERANCE_DB: 0.1 dB residual tolerance on segment RMS targets.
  * PEAK_TOLERANCE: 1e-6 relative tolerance on the output peak ceiling.
  * Chunked vs whole-buffer application is bit-identical.
"""
from .planner import (
    Conflict,
    Plan,
    PlanResult,
    PlannedSegment,
    Segment,
    db_to_lin,
    input_digest,
    lin_to_db,
    plan_gains,
    PEAK_TOLERANCE,
    RMS_TOLERANCE_DB,
    SILENCE_RMS,
)
from .apply import PlanInvalidError, apply_plan, apply_plan_to_file

PLANNER_LATENCY = "offline: requires the complete input signal"
APPLY_LATENCY_SAMPLES = 0

__all__ = [
    "Conflict",
    "Plan",
    "PlanResult",
    "PlannedSegment",
    "Segment",
    "db_to_lin",
    "input_digest",
    "lin_to_db",
    "plan_gains",
    "PlanInvalidError",
    "apply_plan",
    "apply_plan_to_file",
    "PEAK_TOLERANCE",
    "RMS_TOLERANCE_DB",
    "SILENCE_RMS",
    "PLANNER_LATENCY",
    "APPLY_LATENCY_SAMPLES",
]
