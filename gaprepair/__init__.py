"""Gap repair for sampled signals: causal (online) and offline modes."""

from .core import (
    MODE_OFFLINE,
    MODE_ONLINE,
    NUMERICAL_TOLERANCE,
    OFFLINE_LATENCY,
    ONLINE_LATENCY,
    GapReport,
    RepairConfig,
    RepairResult,
    find_gaps,
    is_missing,
    repair_all,
    restore_segments,
)
from .calibration import (
    ModelRegistry,
    ModelVersion,
    calibrate_and_register,
    calibrate_error_scale,
    synthetic_occlusion_experiment,
)
from . import fixtures

__all__ = [
    "MODE_OFFLINE",
    "MODE_ONLINE",
    "NUMERICAL_TOLERANCE",
    "OFFLINE_LATENCY",
    "ONLINE_LATENCY",
    "GapReport",
    "RepairConfig",
    "RepairResult",
    "find_gaps",
    "is_missing",
    "repair_all",
    "restore_segments",
    "ModelRegistry",
    "ModelVersion",
    "calibrate_and_register",
    "calibrate_error_scale",
    "synthetic_occlusion_experiment",
    "fixtures",
]
