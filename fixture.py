"""Local test fixture (no audio devices involved).

Provides everything the aligner is not allowed to invent on its own:

* the fractional-delay FIR coefficients (windowed-sinc table),
* the sample rate and multi-channel sample arrays,
* the declared per-channel delays and the maximum delay,
* the numeric tolerances used by the tests.
"""

import math
import random

# --- declared global parameters -------------------------------------------

SAMPLE_RATE = 48_000          # Hz, nominal rate of the provided arrays
KERNEL_LENGTH = 16            # fractional-delay FIR length (taps)
KERNEL_GROUP_DELAY = (KERNEL_LENGTH - 1) / 2  # 7.5 samples
NUM_CHANNELS = 3
DECLARED_DELAYS = [12.0, 9.25, 30.5]   # per-channel base delay, in samples
MAX_DELAY = 64.0              # any delay (base or scheduled) stays <= this
TOLERANCE = 1e-9              # numeric tolerance for float comparisons
CENTROID_TOLERANCE = 0.05     # samples, for impulse-position checks


# --- fractional-delay coefficients -----------------------------------------

def _sinc(x: float) -> float:
    if x == 0.0:
        return 1.0
    return math.sin(math.pi * x) / (math.pi * x)


def fractional_kernel(frac: float) -> list[float]:
    """FIR coefficients for a delay of KERNEL_GROUP_DELAY + frac samples.

    Windowed-sinc (Hann window), DC-normalised.  The returned kernel h
    implements y[n] = sum_k h[k] * x[n - k] ~= x(n - KERNEL_GROUP_DELAY - frac).
    """
    if not 0.0 <= frac < 1.0:
        raise ValueError(f"frac must be in [0, 1), got {frac!r}")
    klen = KERNEL_LENGTH
    h = []
    for k in range(klen):
        x = k - KERNEL_GROUP_DELAY - frac
        window = 0.5 - 0.5 * math.cos(2.0 * math.pi * k / (klen - 1))  # Hann
        h.append(_sinc(x) * window)
    norm = sum(h)
    return [c / norm for c in h]


# --- sample arrays -----------------------------------------------------------

def make_multichannel_input(length: int = 600) -> list[list[float]]:
    """Deterministic 3-channel test signal (sine / sine / seeded noise)."""
    rng = random.Random(20241001)
    t = [n / SAMPLE_RATE for n in range(length)]
    return [
        [math.sin(2.0 * math.pi * 440.0 * tt) for tt in t],
        [0.5 * math.sin(2.0 * math.pi * 1000.0 * tt + 0.3) for tt in t],
        [0.25 * rng.uniform(-1.0, 1.0) for _ in range(length)],
    ]


def impulse_input(position: int, length: int) -> list[float]:
    """Single unit impulse, used to verify delay positions."""
    x = [0.0] * length
    x[position] = 1.0
    return x
