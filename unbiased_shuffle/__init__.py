"""无偏 Fisher-Yates 洗牌工具包：显式随机源、可暂停状态、排名定位、可恢复外存洗牌。"""
from .prng import CountingSource, randbelow
from .shuffle import ShuffleState
from .ranking import (
    num_partial_permutations,
    rank_partial,
    rank_permutation,
    unrank_partial,
    unrank_permutation,
)
from .external import ExternalShuffle, InjectedCrash

__all__ = [
    "CountingSource",
    "randbelow",
    "ShuffleState",
    "num_partial_permutations",
    "rank_partial",
    "rank_permutation",
    "unrank_partial",
    "unrank_permutation",
    "ExternalShuffle",
    "InjectedCrash",
]
