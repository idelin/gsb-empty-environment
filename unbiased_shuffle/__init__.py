"""Unbiased shuffling and sampling toolkit.

All randomness is injected by the caller: every algorithm takes an ``rng``
object (see :mod:`unbiased_shuffle.prng`) instead of creating one, so seeds,
states and sample counts are always under caller control.
"""

from .prng import SplitMix64Source, RandomModuleSource
from .rangesample import randbelow
from .shuffle import PausableShuffle, fisher_yates
from .unrank import num_partial_permutations, unrank_partial, rank_partial
from .external import (
    BlockedRecordStore,
    ExternalShuffler,
    CorruptionError,
    SimulatedCrash,
    initialize_record_file,
)

__all__ = [
    "SplitMix64Source",
    "RandomModuleSource",
    "randbelow",
    "PausableShuffle",
    "fisher_yates",
    "num_partial_permutations",
    "unrank_partial",
    "rank_partial",
    "BlockedRecordStore",
    "ExternalShuffler",
    "CorruptionError",
    "SimulatedCrash",
    "initialize_record_file",
]
