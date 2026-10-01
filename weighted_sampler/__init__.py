"""Exact weighted sampling with a Fenwick tree over rational weights.

Public API:
    WeightedSampler   -- non-negative, updatable rational weights; tree-based sampling
    Transaction       -- snapshot-versioned batch draws without replacement, with rollback
    SeededRandomSource-- caller-injected, seed-recorded random source
    BitSource         -- deterministic bit source (testing / manual recomputation)
    randbelow         -- uniform integer in [0, n) via rejection sampling (no modulo bias)
    sequence_probability -- exact conditional probability of an ordered without-replacement draw
"""

from .sources import BitSource, ExhaustedBitsError, SeededRandomSource, randbelow
from .sampler import (
    EmptySamplerError,
    StaleTransactionError,
    Transaction,
    WeightedSampler,
    sequence_probability,
)

__all__ = [
    "BitSource",
    "EmptySamplerError",
    "ExhaustedBitsError",
    "SeededRandomSource",
    "StaleTransactionError",
    "Transaction",
    "WeightedSampler",
    "randbelow",
    "sequence_probability",
]
