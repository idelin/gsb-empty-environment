"""Multi-field threshold TopK fusion over local synthetic UTF-8 documents.

Pure Python 3.11 standard library; no search service, no full-text library.
"""

from .engine import (
    Engine,
    Page,
    SearchResult,
    Snapshot,
    SnapshotNotFoundError,
)
from .fusion import (
    AssumptionViolatedError,
    FieldScore,
    NegativeWeightError,
    ScoredDoc,
    UnknownFieldError,
    aggregate,
    exhaustive_topk,
    score_document,
    threshold_topk,
    validate_weights,
)
from .index import Document, FieldQueryView, Index
from .normalize import FieldStats, normalize
from .tokenizer import tokenize

__version__ = "1.0.0"

__all__ = [
    "AssumptionViolatedError",
    "Document",
    "Engine",
    "FieldQueryView",
    "FieldScore",
    "FieldStats",
    "Index",
    "NegativeWeightError",
    "Page",
    "ScoredDoc",
    "SearchResult",
    "Snapshot",
    "SnapshotNotFoundError",
    "UnknownFieldError",
    "aggregate",
    "exhaustive_topk",
    "normalize",
    "score_document",
    "threshold_topk",
    "tokenize",
    "validate_weights",
]
