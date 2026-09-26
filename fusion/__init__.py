"""Multi-field threshold TopK fusion (stdlib only, Python 3.11)."""

from .tokenizer import tokenize
from .corpus import Document, load_corpus
from .index import Index, Snapshot, FieldPlan, Paginator, Posting, FieldStats
from .normalize import normalize_series, normalize_value, EPSILON
from .fusion import (
    fuse_topk,
    exhaustive_fuse,
    exact_score,
    greedy_upper_bound,
    validate_weights,
    NegativeWeightError,
    AllZeroWeightsError,
    FuseResult,
    Hit,
)

__all__ = [
    "tokenize",
    "Document",
    "load_corpus",
    "Index",
    "Snapshot",
    "FieldPlan",
    "Paginator",
    "Posting",
    "FieldStats",
    "normalize_series",
    "normalize_value",
    "EPSILON",
    "fuse_topk",
    "exhaustive_fuse",
    "exact_score",
    "greedy_upper_bound",
    "validate_weights",
    "NegativeWeightError",
    "AllZeroWeightsError",
    "FuseResult",
    "Hit",
]
