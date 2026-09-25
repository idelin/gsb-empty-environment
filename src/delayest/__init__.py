"""Sliding-window normalized cross-correlation relative-delay estimator.

Estimates pairwise channel relative delays per time window. Missing samples
are handled via intersection masks; each channel may be polarity-flipped; the
delay is allowed to drift linearly in time. Per-window NCC peaks that tie are
kept as candidates, and a finite-state dynamic program over the full time
trajectory selects one continuous delay path (windows are never maximized
independently). Low-information windows stay unknown, and evidence is
retained so that a window arriving late can revise earlier and later path
choices when the trajectory is re-solved.

Only the Python standard library is used.
"""

from .ncc import LagPeak, WindowEvidence, window_evidence, ncc_curve
from .dp import TrajectoryStep, solve_trajectory
from .estimator import (
    DelayEstimator,
    WindowDelay,
    DelayResult,
    estimate_delays,
    slice_windows,
)

__all__ = [
    "LagPeak",
    "WindowEvidence",
    "window_evidence",
    "ncc_curve",
    "TrajectoryStep",
    "solve_trajectory",
    "DelayEstimator",
    "WindowDelay",
    "DelayResult",
    "estimate_delays",
    "slice_windows",
]
