"""facetlib：纯标准库的多值分面统计引擎（位图 + 自排除 + 快照）。"""

from .documents import Doc, generate_docs, load_docs, parse_doc, render_doc, write_docs
from .index import FacetIndex, Snapshot, build_index
from .query import MISSING, EvalStats, FacetResult, Query, evaluate, text_query
from .tokenizer import tokenize
from .verify import reference_counts, verify_all_combinations

__all__ = [
    "Doc",
    "EvalStats",
    "FacetIndex",
    "FacetResult",
    "MISSING",
    "Query",
    "Snapshot",
    "build_index",
    "evaluate",
    "generate_docs",
    "load_docs",
    "parse_doc",
    "reference_counts",
    "render_doc",
    "text_query",
    "tokenize",
    "verify_all_combinations",
    "write_docs",
]
