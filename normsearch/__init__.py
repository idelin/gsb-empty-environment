"""normsearch: normalization-aware local text search with UTF-16 mapping."""

from .highlight import merge_ranges, render_html, utf16_slice
from .index import Index, Segment, VersionMismatchError
from .normalize import (
    NormalizedText,
    normalize_piece,
    normalize_with_map,
    utf16_len,
)

__all__ = [
    "Index",
    "NormalizedText",
    "Segment",
    "VersionMismatchError",
    "merge_ranges",
    "normalize_piece",
    "normalize_with_map",
    "render_html",
    "utf16_len",
    "utf16_slice",
]
