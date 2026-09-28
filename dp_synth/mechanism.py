"""Laplace mechanism with a seeded, thread-safe random source."""

from __future__ import annotations

import math
import random
import threading
from typing import Dict


class MechanismError(RuntimeError):
    pass


class Rng:
    """Serialize access to a seeded ``random.Random`` so draws are reproducible."""

    def __init__(self, seed: int = 0) -> None:
        self._rng = random.Random(seed)
        self._lock = threading.Lock()

    def laplace(self, scale: float) -> float:
        if scale <= 0 or math.isinf(scale):
            raise MechanismError(f"invalid Laplace scale: {scale}")
        with self._lock:
            exp = self._rng.expovariate(1.0 / scale)
            sign = -1.0 if self._rng.random() < 0.5 else 1.0
        return sign * exp


def laplace_mechanism(true_value: float, epsilon: float, sensitivity: float, rng: Rng) -> float:
    if sensitivity < 0:
        raise MechanismError("sensitivity must be non-negative")
    scale = sensitivity / epsilon
    return float(true_value) + rng.laplace(scale)


def noisy_histogram(true_groups: Dict[str, int], epsilon: float, rng: Rng) -> Dict[str, float]:
    """Independent Laplace noise per group; each unit contributes to one bucket."""
    return {key: float(count) + rng.laplace(1.0 / epsilon) for key, count in true_groups.items()}
