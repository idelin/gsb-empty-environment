"""Privacy mechanisms. Only the Laplace mechanism (pure epsilon-DP, delta = 0)."""

from __future__ import annotations

import math
import random
import threading


class LaplaceMechanism:
    """Laplace noise with scale ``sensitivity / epsilon``.

    The injected RNG is process-local and guarded by a lock so threaded callers
    never draw the same stream state.
    """

    name = "laplace"

    def __init__(self, seed: int | None = None) -> None:
        self._rng = random.Random(seed)
        self._lock = threading.Lock()

    def _uniform(self) -> float:
        with self._lock:
            u = self._rng.random()
            while u <= 0.0:  # log(0) guard; random() is in [0, 1)
                u = self._rng.random()
        return u

    def release(self, true_value: float, sensitivity: float, epsilon: float) -> float:
        if sensitivity < 0 or epsilon <= 0:
            raise ValueError("sensitivity must be >= 0 and epsilon > 0")
        scale = sensitivity / epsilon
        u = self._uniform()
        # Inverse CDF of Laplace(0, b): x = b*ln(2u) for u<.5, else -b*ln(2(1-u)).
        if u < 0.5:
            noise = scale * math.log(2.0 * u)
        else:
            noise = -scale * math.log(2.0 * (1.0 - u))
        return true_value + noise
