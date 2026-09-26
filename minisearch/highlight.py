"""Highlight original code-point ranges as escaped HTML.

Overlapping (and adjacent, per request semantics) ranges are merged
first, so no region of the original text is ever wrapped twice. Every
byte of output text is passed through :func:`html.escape`; therefore
literal ``<script>`` content is displayed as text and can never execute
as a tag. Each wrapper carries ``data-utf16-start`` / ``data-utf16-end``
attributes so the visible highlight remains traceable to the original
UTF-16 interval even after escaping (escaping never changes offsets).
"""

from __future__ import annotations

import html

from .normalize import utf16_prefix


def merge_ranges(ranges: list[tuple[int, int]]) -> list[tuple[int, int]]:
    """Merge overlapping ranges. Empty/negative ranges are dropped."""
    valid = sorted((a, b) for a, b in ranges if 0 <= a < b)
    merged: list[list[int]] = []
    for start, end in valid:
        if merged and start <= merged[-1][1]:
            if end > merged[-1][1]:
                merged[-1][1] = end
        else:
            merged.append([start, end])
    return [(start, end) for start, end in merged]


def merge_utf16_ranges(ranges: list[tuple[int, int]]) -> list[tuple[int, int]]:
    return merge_ranges(list(ranges))


def to_utf16_ranges(text: str, cp_ranges: list[tuple[int, int]]) -> list[tuple[int, int]]:
    prefix = utf16_prefix(text)
    return merge_ranges([(prefix[a], prefix[b]) for a, b in cp_ranges])


def render_html(
    text: str,
    cp_ranges: list[tuple[int, int]],
    class_name: str = "match",
    tag: str = "mark",
) -> str:
    """Render ``text`` with ranges wrapped in ``<mark>``; everything escaped."""
    safe_tag = html.escape(tag, quote=True)
    safe_class = html.escape(class_name, quote=True)
    merged = merge_ranges(cp_ranges)
    pieces: list[str] = []
    cursor = 0
    prefix = utf16_prefix(text)
    for start, end in merged:
        if start < cursor:
            continue  # defensive: merged ranges are already ordered
        pieces.append(html.escape(text[cursor:start]))
        pieces.append(
            f'<{safe_tag} class="{safe_class}" '
            f'data-utf16-start="{prefix[start]}" data-utf16-end="{prefix[end]}">'
            f"{html.escape(text[start:end])}</{safe_tag}>"
        )
        cursor = end
    pieces.append(html.escape(text[cursor:]))
    return "".join(pieces)
