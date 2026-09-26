"""Local Unicode-aware retrieval: normalization, versioned segments, highlights."""

from .highlight import merge_ranges, merge_utf16_ranges, render_html, to_utf16_ranges
from .index import Hit, Index, Segment, find_occurrences
from .normalize import Alignment, Link, normalize_document, token_spans, utf16_prefix

__all__ = [
    "Alignment",
    "Link",
    "Hit",
    "Index",
    "Segment",
    "find_occurrences",
    "merge_ranges",
    "merge_utf16_ranges",
    "render_html",
    "to_utf16_ranges",
    "normalize_document",
    "token_spans",
    "utf16_prefix",
]
