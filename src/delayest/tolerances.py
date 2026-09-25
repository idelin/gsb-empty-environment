"""Declared delay resolution and numerical tolerances.

Delay convention
----------------
For two channels ``a`` and ``b``, the reported lag ``d`` (in samples, float)
follows::

    b[n] ~= polarity * a[n - d]

``d > 0`` means channel ``b`` is delayed relative to ``a`` (events in ``b``
arrive later). Seconds are obtained as ``d / fs``. Sub-sample delays are
refined by parabolic interpolation around the integer NCC grid; outside
refinement the native resolution is one sample.
"""

from __future__ import annotations

# ---------------------------------------------------------------------------
# Delay resolution
# ---------------------------------------------------------------------------
# Native NCC lag-grid resolution, in samples.
LAG_UNIT_SAMPLES = 1.0
# Half-grid quantization bound on the integer lag search, in samples.
GRID_HALF_BIN_SAMPLES = 0.5
# Default conservative 1-sigma-ish sub-sample uncertainty bound, in samples.
DEFAULT_LAG_UNCERTAINTY_SAMPLES = 0.5
# Drift (velocity) is modelled on the same finite grid: samples per window.
VELOCITY_UNIT_SAMPLES_PER_WINDOW = 1.0

# ---------------------------------------------------------------------------
# Numerical tolerances
# ---------------------------------------------------------------------------
# Values outside [-1, 1] after normalized correlation are clamped to the
# endpoint when the excess is no larger than this; larger excess is a bug.
NCC_CLAMP_TOL = 1e-9
# Two peak scores count as tied when they differ by no more than this.
PEAK_TIE_TOL = 1e-9
# Minimum absolute standardized amplitude (|x - mean| / sqrt(variance)) sum
# contributor count protection: windows with fewer valid overlapping pairs
# than this carry no evidence.
MIN_OVERLAP_PAIRS = 8
# Minimum NCC of the strongest peak for a window to carry any evidence.
MIN_PEAK_NCC = 0.15
# Polarity decision threshold: with |best_neg - best_pos| below this the
# polarity is reported as unknown (no evidence for a sign flip).
POLARITY_TOL = 1e-9
