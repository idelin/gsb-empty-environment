"""Local sample-array fixtures.  No audio devices are involved."""

import math
import random

SAMPLE_RATE = 48_000.0


def constant(n, level=1.0):
    return [float(level)] * n


def impulse(n, amplitude=1.0, at=0):
    samples = [0.0] * n
    samples[at] = float(amplitude)
    return samples


def sine(n, freq=440.0, sample_rate=SAMPLE_RATE, amplitude=1.0):
    return [amplitude * math.sin(2.0 * math.pi * freq * i / sample_rate)
            for i in range(n)]


def noise(n, seed=1234):
    rng = random.Random(seed)
    return [rng.uniform(-1.0, 1.0) for _ in range(n)]


def moving_average(length):
    """Unity-DC-gain smoothing kernel of the given length."""
    return [1.0 / length] * length
