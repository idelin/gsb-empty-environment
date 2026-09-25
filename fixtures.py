"""Local synthetic fixtures: sample arrays + sample rate, no audio devices.

All declared ground-truth delays, drift rates and numeric tolerances used by
the test-suite live here so the tests assert against explicit constants.
"""

import math
import random

# ---------------------------------------------------------------------------
# Declared acquisition parameters
# ---------------------------------------------------------------------------
SAMPLE_RATE = 8000.0          # Hz
WINDOW = 512                  # samples per analysis window
HOP = 256                     # samples between window starts
MAX_LAG = 64                  # samples, search range is [-MAX_LAG, MAX_LAG]
MIN_OVERLAP = 64              # valid sample pairs required per candidate
MIN_SCORE = 0.2               # min |NCC| for a window to be informative

# ---------------------------------------------------------------------------
# Declared ground truth for the drifting two-channel fixture
# ---------------------------------------------------------------------------
N_SAMPLES = 6000
DELAY0 = 3.0                  # samples: delay of y vs x at t = 0
DRIFT = 0.0015                # samples per sample (linear drift)
NOISE_STD = 0.02              # additive noise on y
SEED = 7

# ---------------------------------------------------------------------------
# Declared numeric tolerances
# ---------------------------------------------------------------------------
TRAJ_TOL = 0.75               # samples: per-window |est - truth| on drift track
INT_DELAY_TOL = 0.5           # samples: sub-sample accuracy for static delay
TIE_SCORE_TOL = 1e-6          # |NCC| equality tolerance for "equal-height" peaks


def true_delay(t):
    """Ground-truth delay (samples) of the drifting fixture at time t."""
    return DELAY0 + DRIFT * t


def make_white_noise(n, seed=SEED):
    rng = random.Random(seed)
    return [rng.gauss(0.0, 1.0) for _ in range(n)]


def fractional_delay_pair(n=N_SAMPLES, delay_fn=true_delay, polarity=1,
                          noise_std=NOISE_STD, seed=SEED):
    """Two-channel pair with y[t] = polarity * x[t - d(t)] + noise.

    Fractional delays are realised by linear interpolation of the reference.
    Returns ``(x, y)`` as plain lists of floats.
    """
    pad = 4 * MAX_LAG
    x = make_white_noise(n + pad, seed)
    rng = random.Random(seed + 1)
    y = []
    for t in range(n):
        d = delay_fn(t)
        t0 = t - d
        i = math.floor(t0)
        frac = t0 - i
        v = (1.0 - frac) * x[i] + frac * x[i + 1]
        y.append(polarity * v + noise_std * rng.gauss(0.0, 1.0))
    return x, y


def make_sine(n, period, phase=0.0):
    return [math.sin(2.0 * math.pi * t / period + phase) for t in range(n)]


def full_mask(n):
    return [True] * n


def mask_with_gap(n, gap_start, gap_end):
    """Valid everywhere except [gap_start, gap_end)."""
    return [not (gap_start <= t < gap_end) for t in range(n)]


def window_starts(n, window=WINDOW, hop=HOP):
    return list(range(0, n - window + 1, hop))
