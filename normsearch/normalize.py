"""Text normalization with an exact many-to-many position map.

Pipeline, applied independently to each original Python character:

1. Control / format characters (Unicode general categories Cc and Cf, e.g.
   U+0000..U+001F, U+007F..U+009F, soft hyphen U+00AD, zero-width space
   U+200B) are deleted. They contribute zero normalized characters.
2. ``str.casefold()`` handles case, including expansions such as
   "ß" -> "ss" and "İ" -> "i" + U+0307.
3. ``unicodedata.normalize("NFKD", ...)`` decomposes precomposed characters,
   e.g. "é" -> "e" + U+0301 and "ﬁ" -> "fi".
4. Combining marks (characters with a non-zero canonical combining class)
   are dropped, so accented and unaccented letters compare equal.

Because one source character may produce several output characters
(expansion / decomposition) or none (deleted control / dropped mark),
positions cannot be translated by adding a constant length delta.
Instead every normalized output character carries the *UTF-16 code-unit*
span of the single original character that produced it. These per-character
spans are the composable many-to-many relation: any normalized interval maps
to the union of the spans of its characters, which is exactly the original
UTF-16 interval to highlight.
"""

from __future__ import annotations

import unicodedata
from dataclasses import dataclass

_DELETED_CATEGORIES = frozenset({"Cc", "Cf"})


def is_deleted_char(ch: str) -> bool:
    """Return True for control/format chars removed by normalization."""
    return unicodedata.category(ch) in _DELETED_CATEGORIES


def normalize_piece(ch: str) -> str:
    """Normalize one original character into zero or more output chars."""
    if is_deleted_char(ch):
        return ""
    folded = ch.casefold()
    decomposed = unicodedata.normalize("NFKD", folded)
    kept = []
    for piece in decomposed:
        if unicodedata.combining(piece):
            continue
        if is_deleted_char(piece):
            continue
        kept.append(piece)
    return "".join(kept)


def utf16_len(ch: str) -> int:
    """UTF-16 code units occupied by one Python character."""
    return 2 if ord(ch) > 0xFFFF else 1


@dataclass(frozen=True)
class NormalizedText:
    """A normalized string with a character-level source map.

    ``source_spans[i]`` is the half-open UTF-16 code-unit span
    ``(start, end)`` of the original character that produced
    ``normalized[i]``. Several adjacent entries may share the same span
    (expansion); deleted chars have no entry at all.
    """

    original: str
    normalized: str
    source_spans: tuple[tuple[int, int], ...]

    def __post_init__(self) -> None:
        if len(self.normalized) != len(self.source_spans):
            raise ValueError("normalized text and source spans differ in length")

    def map_range(self, start: int, end: int) -> tuple[int, int]:
        """Map normalized half-open index range [start, end) to UTF-16 units."""
        if start < 0 or end < start or end > len(self.source_spans):
            raise IndexError(f"invalid normalized range: [{start}, {end})")
        if start == end:
            raise IndexError("cannot map an empty range")
        return self.source_spans[start][0], self.source_spans[end - 1][1]


def normalize_with_map(text: str) -> NormalizedText:
    """Normalize ``text`` and attach per-output-char UTF-16 source spans."""
    out_chars: list[str] = []
    spans: list[tuple[int, int]] = []
    unit = 0
    for ch in text:
        start = unit
        unit += utf16_len(ch)
        piece = normalize_piece(ch)
        for out_ch in piece:
            out_chars.append(out_ch)
            spans.append((start, unit))
    return NormalizedText(text, "".join(out_chars), tuple(spans))
