"""Rolling per-scene, per-band noise-floor estimation.

Only the Python standard library is used. No audio devices are touched;
sample arrays and the sample rate come from local fixtures.
"""

from .limits import LIMITS, Limits
from .model import (
    BandModel,
    Detection,
    Estimate,
    NoiseFloorBank,
    Sample,
    Threshold,
    MISSING,
    TRANSIENT,
    VALID,
)
from .quantile import SortedMultiset

__all__ = [
    "LIMITS",
    "Limits",
    "BandModel",
    "Detection",
    "Estimate",
    "NoiseFloorBank",
    "Sample",
    "Threshold",
    "SortedMultiset",
    "MISSING",
    "TRANSIENT",
    "VALID",
]
