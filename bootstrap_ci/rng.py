"""Caller-injected randomness with deterministic per-sample-ID streams."""
from __future__ import annotations

import hashlib
import random


class RandomSource:
    """Randomness injected by the caller.

    The source owns a seed and hands out independent streams keyed by
    ``sample_id``.  Each stream depends only on ``(seed, sample_id)`` — never
    on call order — so replicates may be evaluated in any order, including in
    parallel, without changing results.
    """

    def __init__(self, seed: int):
        if not isinstance(seed, int):
            raise TypeError(f"seed must be an int, got {type(seed).__name__}")
        self.seed = seed

    def stream(self, sample_id) -> random.Random:
        """Deterministic stream for one resample replicate."""
        key = f"bootstrap_ci:{self.seed}:{sample_id!r}".encode("utf-8")
        derived = int.from_bytes(hashlib.sha256(key).digest()[:8], "big")
        return random.Random(derived)

    def __repr__(self):
        return f"RandomSource(seed={self.seed})"
