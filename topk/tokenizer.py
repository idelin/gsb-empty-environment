"""Deterministic UTF-8 tokenizer (standard library only).

Tokenization rules
------------------
1. A token is a maximal run of "word characters", where a word character is
   any Unicode code point whose category starts with ``L`` (letter), ``M``
   (mark, so combining accents stay attached) or ``N`` (number), or the
   underscore ``_``. Every other code point (whitespace, punctuation, CJK
   separators, emoji, ...) is a separator.
2. Token *identity* is the case-folded form (``str.casefold``), so ``Apple``,
   ``APPLE`` and ``apple`` are the same word, and ``Straße`` folds to
   ``strasse``.
3. The *display form* is the original text span, preserved verbatim. Multiple
   display aliases of the same normalized word are aggregated by the index.
"""

from __future__ import annotations

import unicodedata
from typing import Iterator, NamedTuple


class Token(NamedTuple):
    display: str     # original text span
    normalized: str  # case-folded identity


def _is_word_char(ch: str) -> bool:
    if ch == "_":
        return True
    return unicodedata.category(ch)[0] in ("L", "M", "N")


def tokenize(text: str) -> Iterator[Token]:
    """Yield ``Token`` for every token in ``text`` in document order."""
    buf: list[str] = []
    for ch in text:
        if _is_word_char(ch):
            buf.append(ch)
        elif buf:
            display = "".join(buf)
            yield Token(display, display.casefold())
            buf.clear()
    if buf:
        display = "".join(buf)
        yield Token(display, display.casefold())
