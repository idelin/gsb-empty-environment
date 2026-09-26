"""Pure-stdlib sample helpers: true energy, peak, and input digests.

All functions operate on plain sequences of floats (lists or
``array.array``); no audio devices or external codecs are involved.
"""

from __future__ import annotations

import hashlib
import math
import struct
from typing import Sequence

_DIGEST_CHUNK = 65536


def region_rms(samples: Sequence[float], start: int, end: int) -> float:
    """True RMS energy of ``samples[start:end]``.

    Works for arbitrarily short regions (including 1-sample regions) and for
    overlap/fade regions: the energy is computed from the actual samples,
    never from segment-level approximations.
    """
    n = end - start
    if n <= 0:
        return 0.0
    acc = 0.0
    for i in range(start, end):
        x = samples[i]
        acc += x * x
    return math.sqrt(acc / n)


def region_peak(samples: Sequence[float], start: int, end: int) -> float:
    """True absolute peak of ``samples[start:end]`` (0.0 for empty regions)."""
    peak = 0.0
    for i in range(start, end):
        a = abs(samples[i])
        if a > peak:
            peak = a
    return peak


def input_digest(samples: Sequence[float], sample_rate: int) -> str:
    """SHA-256 digest binding a plan to its exact input.

    Samples are quantized to little-endian float32 before hashing so the
    digest is stable across platforms and matches the on-disk precision of
    the rendered output. Any correction to the source audio (or a change of
    sample rate / length) changes the digest and therefore invalidates every
    plan built from the old input.
    """
    h = hashlib.sha256()
    h.update(struct.pack("<qq", sample_rate, len(samples)))
    for lo in range(0, len(samples), _DIGEST_CHUNK):
        chunk = samples[lo:lo + _DIGEST_CHUNK]
        h.update(struct.pack("<%df" % len(chunk), *chunk))
    return h.hexdigest()
