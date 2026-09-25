"""Gap repair for sampled signals: linear / autoregressive, online / offline."""

from .calibrate import CalibrationResult, synthetic_occlusion_experiment
from .config import RepairConfig
from .constants import (CONDITION_TOLERANCE, NUMERIC_TOLERANCE,
                        online_latency_samples)
from .fixtures import load_fixture
from .gaps import Gap, find_gaps
from .registry import ModelRegistry, ModelVersion
from .repair import (MASK_KNOWN, MASK_REPAIRED, MASK_UNKNOWN, GapRepair,
                     Mode, RepairResult, repair, restore_segment)

__all__ = [
    "CalibrationResult",
    "CONDITION_TOLERANCE",
    "Gap",
    "GapRepair",
    "MASK_KNOWN",
    "MASK_REPAIRED",
    "MASK_UNKNOWN",
    "Mode",
    "ModelRegistry",
    "ModelVersion",
    "NUMERIC_TOLERANCE",
    "RepairConfig",
    "RepairResult",
    "find_gaps",
    "load_fixture",
    "online_latency_samples",
    "repair",
    "restore_segment",
    "synthetic_occlusion_experiment",
]
