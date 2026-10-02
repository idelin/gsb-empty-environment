"""Dynamics curve compilation and processing (standard library only).

- dyncurve.curve      static curve compile/verify/invert (stateless)
- dyncurve.envelope   envelope detection + gain smoothing (stateful)
- dyncurve.processor  multi-channel wiring, sidechain, hot reload
- dyncurve.fixtures   deterministic local sample arrays + SAMPLE_RATE

Declared total latency: 0 samples (DynamicsProcessor.latency_samples).
Numerical tolerances: see dyncurve.curve (VALUE_TOL, SLOPE_TOL,
INVERSE_TOL, DB_FLOOR).
"""
from .curve import (COMPRESS, EXPAND, DB_FLOOR, INVERSE_TOL, SLOPE_TOL,
                    VALUE_TOL, CompiledCurve, InverseResult, Segment,
                    amp_to_db, compile_curve, db_to_amp)
from .envelope import GainSmoother, PeakDetector, RMSDetector
from .processor import (DetectorConfig, DynamicsProcessor, EnvelopePolicy,
                        SidechainConfig, SmootherConfig)

__all__ = [
    "COMPRESS", "EXPAND", "DB_FLOOR", "INVERSE_TOL", "SLOPE_TOL", "VALUE_TOL",
    "CompiledCurve", "InverseResult", "Segment", "amp_to_db", "compile_curve",
    "db_to_amp", "GainSmoother", "PeakDetector", "RMSDetector",
    "DetectorConfig", "DynamicsProcessor", "EnvelopePolicy", "SidechainConfig",
    "SmootherConfig",
]
