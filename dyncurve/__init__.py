"""dyncurve: static dynamics curves + envelope/gain smoothing + processor.

Declared numeric contract:
  * LATENCY_SAMPLES: end-to-end processor latency in samples (0; all stages
    are causal, zero-lookahead). Hot curve swaps never change it.
  * TOLERANCES: named numeric tolerances used by the library and tests.
  * DB_FLOOR: levels at/below this are silence; gains stay finite.
"""
from .curve import (CONTINUITY_TOL, DB_FLOOR, SLOPE_TOL, CheckResult,
                    CompiledCurve, Segment, compile_curve)
from .envelope import GainSmoother, PeakDetector, RmsDetector, amplitude_to_db
from .processor import (LATENCY_SAMPLES, DetectorConfig, DynamicsProcessor,
                        SidechainConfig, SmootherConfig)

TOLERANCES = {
    "curve_continuity_db": CONTINUITY_TOL,   # value jumps at piece boundaries
    "curve_slope": SLOPE_TOL,                # slope mismatch across soft knees
    "static_gain_db": 1e-9,                  # compiled curve vs analytic reference
    "sample_exact": 0.0,                     # block-size / state-restore runs are bit-identical
    "hot_swap_gain_db": 1e-6,                # max deviation from the ideal crossfade ramp
    "inverse_db": 1e-9,                      # inverse lookup round-trip
}

__all__ = [
    "Segment", "CompiledCurve", "CheckResult", "compile_curve",
    "PeakDetector", "RmsDetector", "GainSmoother", "amplitude_to_db",
    "DynamicsProcessor", "DetectorConfig", "SmootherConfig", "SidechainConfig",
    "LATENCY_SAMPLES", "DB_FLOOR", "TOLERANCES",
]
