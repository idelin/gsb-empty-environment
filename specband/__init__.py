"""Spectrum-to-band integration with versioned frames and band rules."""

from .bands import Band, BandSet
from .coverage import COVERAGE_MODEL_VERSION, band_weights, bin_width, coverage_weight
from .engine import Correction, Engine, SpectrumFrame
from .integrator import (
    DECLARED_FRAME_LATENCY_MS,
    NUMERICAL_TOLERANCE,
    BandReport,
    IntegrationResult,
    integrate,
)
from .spectrum import power_spectrum

__all__ = [
    "Band",
    "BandSet",
    "BandReport",
    "Correction",
    "COVERAGE_MODEL_VERSION",
    "DECLARED_FRAME_LATENCY_MS",
    "Engine",
    "IntegrationResult",
    "NUMERICAL_TOLERANCE",
    "SpectrumFrame",
    "band_weights",
    "bin_width",
    "coverage_weight",
    "integrate",
    "power_spectrum",
]
