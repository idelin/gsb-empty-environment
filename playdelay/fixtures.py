"""Local fixtures: deterministic sample arrays and the sample rate.

No audio device is involved; the "source" is a deterministic function of the
sample index so every run produces bit-identical payloads.
"""
import math
from array import array

from . import config


def sample_at(i):
    t = i / config.SAMPLE_RATE
    return (0.5 * math.sin(2.0 * math.pi * 440.0 * t)
            + 0.25 * math.sin(2.0 * math.pi * 220.0 * t))


def source_block(start, n):
    """n source samples starting at absolute sample index `start`."""
    return array('f', (sample_at(i) for i in range(start, start + n)))
