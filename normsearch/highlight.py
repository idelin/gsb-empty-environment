"""Highlight merging, UTF-16 boundary conversion and escaped HTML rendering."""

from __future__ import annotations

import html

from .normalize import utf16_len


def merge_ranges(
    ranges: list[tuple[int, int]] | tuple[tuple[int, int], ...],
) -> list[tuple[int, int]]:
    """Merge overlapping or adjacent UTF-16 code-unit ranges.

    Adjacent ranges are merged too: once normalized characters originating
    from the same expansion group are both highlighted, the rendered marks
    must not be split in the middle of an original character.
    """
    ordered = sorted(ranges)
    merged: list[tuple[int, int]] = []
    for start, end in ordered:
        if start > end:
            raise ValueError(f"inverted range: [{start}, {end})")
        if not merged or start > merged[-1][1]:
            merged.append((start, end))
        elif end > merged[-1][1]:
            merged[-1] = (merged[-1][0], end)
    return merged


def utf16_boundaries(text: str) -> dict[int, int]:
    """Map every UTF-16 code-unit character boundary to a Python str index."""
    boundary_to_index: dict[int, int] = {0: 0}
    unit = 0
    for index, ch in enumerate(text):
        unit += utf16_len(ch)
        boundary_to_index[unit] = index + 1
    return boundary_to_index


def utf16_slice(text: str, start: int, end: int) -> str:
    """Slice ``text`` by half-open UTF-16 code-unit offsets."""
    boundaries = utf16_boundaries(text)
    try:
        return text[boundaries[start]:boundaries[end]]
    except KeyError as exc:  # pragma: no cover - defensive
        raise IndexError(f"UTF-16 boundary outside text: {exc}") from None


def render_html(text: str, ranges: list[tuple[int, int]]) -> str:
    """Render ``text`` with ranges wrapped in <mark>.

    Original text is escaped with ``html.escape`` before any markup is
    inserted, so source such as ``<script>`` can never be executed as HTML.
    Ranges are UTF-16 code-unit offsets and may cover astral characters
    (surrogate pairs are never split).
    """
    boundaries = utf16_boundaries(text)
    cursor = 0
    parts: list[str] = []
    for start, end in merge_ranges(ranges):
        py_start = boundaries[start]
        py_end = boundaries[end]
        parts.append(html.escape(text[cursor:py_start], quote=True))
        parts.append("<mark>")
        parts.append(html.escape(text[py_start:py_end], quote=True))
        parts.append("</mark>")
        cursor = py_end
    parts.append(html.escape(text[cursor:], quote=True))
    return "".join(parts)
