"""Reproducible discrete noise, drawn exactly once per measurement key.

Mechanism: discrete Laplace (a.k.a. two-sided geometric) noise,
    noise = G1 - G2,  Gi ~ Geometric(p),  p = 1 - exp(-1/scale),
with scale = sensitivity / epsilon. Given the same master seed and key the
draw is bit-for-bit reproducible; the ledger persists every draw so a
repeated publication reuses the stored noise and can never silently
re-sample until a "nice" value appears.
"""
from __future__ import annotations

import hashlib
import json
import math
import random
from fractions import Fraction
from pathlib import Path


def discrete_laplace(rng: random.Random, scale: Fraction) -> int:
    """Draw an integer noise value with the given L1 scale."""
    p = 1.0 - math.exp(-1.0 / float(scale))

    def geometric() -> int:
        failures = 0
        while rng.random() >= p:
            failures += 1
        return failures

    return geometric() - geometric()


def _key_seed(master_seed: int, key: str) -> int:
    digest = hashlib.sha256(f"{master_seed}|{key}".encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big")


class NoiseLedger:
    """Append-only store of noise draws keyed by measurement name.

    noisy_value() returns the stored value if the key was ever published
    before (reused=True); it never re-draws. The ledger can be persisted
    to JSON so a separate process reproduces exactly the same release.
    """

    def __init__(self, master_seed: int, path=None):
        self.master_seed = master_seed
        self.path = Path(path) if path else None
        self.entries: dict = {}
        if self.path and self.path.exists():
            self.entries = json.loads(self.path.read_text())["entries"]

    def _save(self):
        if self.path:
            payload = {"master_seed": self.master_seed, "entries": self.entries}
            self.path.write_text(json.dumps(payload, indent=2, sort_keys=True))

    def noisy_value(self, key: str, true_value: int, scale: Fraction):
        """Return (noisy_value, noise, reused). Draws once, then reuses."""
        if key in self.entries:
            entry = self.entries[key]
            return entry["noisy"], entry["noise"], True
        rng = random.Random(_key_seed(self.master_seed, key))
        noise = discrete_laplace(rng, scale)
        noisy = true_value + noise
        self.entries[key] = {
            "noise": noise,
            "noisy": noisy,
            "scale": str(scale),
        }
        self._save()
        return noisy, noise, False
