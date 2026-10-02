"""Resampling plans.

Every plan resamples *observational units* — whole observations, whole pairs,
or whole blocks — never individual cells inside a unit, so within-unit
correlation is preserved by construction.
"""
from __future__ import annotations

import math

from .errors import UnsupportedMethodError


class IIDPlan:
    """Single independent sample; the observational unit is one observation."""

    kind = "iid"

    def __init__(self, values):
        self.units = tuple(values)
        if len(self.units) < 2:
            raise ValueError("IIDPlan needs at least 2 observations")

    @property
    def n_units(self) -> int:
        return len(self.units)

    def full_sample(self):
        return list(self.units)

    def resample(self, rng):
        n = len(self.units)
        return [self.units[rng.randrange(n)] for _ in range(n)]

    def jackknife(self):
        return [
            [u for j, u in enumerate(self.units) if j != i]
            for i in range(len(self.units))
        ]


class PairedPlan:
    """Paired sample; the observational unit is the pair.

    Pairs are resampled jointly: the two components of a pair are never
    split, so within-pair dependence is preserved in every replicate.
    """

    kind = "paired"

    def __init__(self, pairs):
        self.units = tuple(tuple(p) for p in pairs)
        if len(self.units) < 2:
            raise ValueError("PairedPlan needs at least 2 pairs")
        for pair in self.units:
            if len(pair) != 2:
                raise ValueError("each pair must have exactly 2 components")

    @property
    def n_units(self) -> int:
        return len(self.units)

    def full_sample(self):
        return list(self.units)

    def resample(self, rng):
        n = len(self.units)
        return [self.units[rng.randrange(n)] for _ in range(n)]

    def jackknife(self):
        return [
            [u for j, u in enumerate(self.units) if j != i]
            for i in range(len(self.units))
        ]


class StratifiedPlan:
    """Stratified independent groups; units are resampled *within* each stratum.

    Boundary rules (explicit): strata are never pooled, no unit migrates
    across a stratum boundary, and every replicate preserves each stratum's
    original size.
    """

    kind = "stratified"

    def __init__(self, strata):
        self.strata = {str(label): tuple(values) for label, values in strata.items()}
        if not self.strata:
            raise ValueError("StratifiedPlan needs at least one stratum")
        for label, values in self.strata.items():
            if len(values) < 2:
                raise ValueError(f"stratum {label!r} needs at least 2 observations")

    @property
    def n_units(self) -> int:
        return sum(len(v) for v in self.strata.values())

    def full_sample(self):
        return {label: list(values) for label, values in self.strata.items()}

    def resample(self, rng):
        out = {}
        for label, values in self.strata.items():
            n = len(values)
            out[label] = [values[rng.randrange(n)] for _ in range(n)]
        return out

    def jackknife(self):
        payloads = []
        for label, values in self.strata.items():
            for i in range(len(values)):
                payload = self.full_sample()
                payload[label] = [u for j, u in enumerate(values) if j != i]
                payloads.append(payload)
        return payloads


class MovingBlockPlan:
    """Moving block bootstrap for serially dependent series.

    The observational unit is a block of ``block_length`` consecutive
    observations, so serial correlation inside blocks is preserved.

    Boundary rules (explicit):
      - ``"circular"``: block starts are uniform over 0..n-1 and indices wrap
        modulo n (the series is treated as a circle).
      - ``"truncate"``: block starts are uniform over 0..n-L; no wrap-around.
    In both cases concatenated blocks are truncated to length n.
    """

    kind = "moving_block"

    def __init__(self, series, block_length: int, boundary: str = "circular"):
        self.units = tuple(series)
        if len(self.units) < 2:
            raise ValueError("MovingBlockPlan needs at least 2 observations")
        if boundary not in ("circular", "truncate"):
            raise ValueError(f"boundary must be 'circular' or 'truncate', got {boundary!r}")
        if not isinstance(block_length, int) or not 1 <= block_length <= len(self.units):
            raise ValueError(
                f"block_length must be an int in [1, {len(self.units)}], got {block_length!r}")
        self.block_length = block_length
        self.boundary = boundary

    @property
    def n_units(self) -> int:
        return len(self.units)

    def full_sample(self):
        return list(self.units)

    def resample(self, rng):
        n = len(self.units)
        length = self.block_length
        n_blocks = math.ceil(n / length)
        indices = []
        if self.boundary == "circular":
            for _ in range(n_blocks):
                start = rng.randrange(n)
                indices.extend((start + j) % n for j in range(length))
        else:  # "truncate"
            for _ in range(n_blocks):
                start = rng.randrange(n - length + 1)
                indices.extend(start + j for j in range(length))
        return [self.units[i] for i in indices[:n]]

    def jackknife(self):
        raise UnsupportedMethodError(
            "delete-one jackknife is not defined for the moving block plan; "
            "BCa is unavailable here — use method='percentile'")
