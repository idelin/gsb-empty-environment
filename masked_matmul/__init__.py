"""masked_matmul: 结构掩码下的稀疏矩阵乘,支持事务化增量维护。"""

from .engine import Engine, Transaction
from .errors import (
    BoundsError,
    MaskedMatmulError,
    RetractionError,
    SpillError,
    TransactionStateError,
    UnknownVersionError,
)
from .index import ContributionIndex, SpillStore
from .masks import ComplementMask, ExplicitMask
from .reference import dense_reference
from .state import SparseMatrix

__all__ = [
    "BoundsError",
    "ComplementMask",
    "ContributionIndex",
    "Engine",
    "ExplicitMask",
    "MaskedMatmulError",
    "RetractionError",
    "SparseMatrix",
    "SpillError",
    "SpillStore",
    "Transaction",
    "TransactionStateError",
    "UnknownVersionError",
    "dense_reference",
]
