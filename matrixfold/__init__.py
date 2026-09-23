"""Non-commutative sequence fold of 2x2 integer matrices.

Maintains per-group ordered sequences of 2x2 matrices (ordered by a stable
sort key) and folds each sequence into a single product matrix under a
fixed modulus, using a balanced interval (segment) tree for updates.
"""

from .matrix import IDENTITY, Matrix, multiply, normalize
from .segment_tree import SegmentTree
from .group import FoldGroup
from .engine import Change, ConflictError, FoldEngine, Transaction

__all__ = [
    "IDENTITY",
    "Matrix",
    "multiply",
    "normalize",
    "SegmentTree",
    "FoldGroup",
    "Change",
    "ConflictError",
    "FoldEngine",
    "Transaction",
]
