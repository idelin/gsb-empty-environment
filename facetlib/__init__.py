"""Bitmap facet engine over local UTF-8 text (standard library only)."""

from .engine import (DimensionReport, FacetEngine, FacetReport, Metrics)
from .model import EMPTY, MISSING, Document, TextQuery
from .reference import reference_facets
from .store import DocumentStore, Snapshot
from .tokenizer import tokenize, token_set

__all__ = [
    "DimensionReport", "Document", "DocumentStore", "EMPTY", "FacetEngine",
    "FacetReport", "Metrics", "MISSING", "Snapshot", "TextQuery",
    "reference_facets", "tokenize", "token_set",
]
