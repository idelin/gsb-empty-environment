"""Tokenization rules for local synthetic UTF-8 text.

Rules (deterministic, standard library only):
  1. Input is a ``str`` (already decoded UTF-8).
  2. The whole text is case-folded with ``str.casefold()``.
  3. A character is a *word character* iff its Unicode general category
     starts with ``L`` (any letter) or equals ``Nd`` (decimal digit).
  4. Han ideographs (CJK Unified Ideographs and extensions, plus
     compatibility ideographs) are emitted as single-character tokens,
     because Chinese text is not space-separated.
  5. Every other maximal run of word characters forms one token.
  6. All remaining characters (whitespace, punctuation, symbols) are
     separators and produce no token.

No search service and no full-text library is used anywhere.
"""

from __future__ import annotations

import unicodedata

_HAN_RANGES = (
    (0x3400, 0x4DBF),    # CJK Unified Ideographs Extension A
    (0x4E00, 0x9FFF),    # CJK Unified Ideographs
    (0xF900, 0xFAFF),    # CJK Compatibility Ideographs
    (0x20000, 0x2A6DF),  # CJK Unified Ideographs Extension B
)


def _is_han(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in _HAN_RANGES)


def _is_word_char(ch: str) -> bool:
    cat = unicodedata.category(ch)
    return cat[0] == "L" or cat == "Nd"


def tokenize(text: str) -> list[str]:
    """Split *text* into tokens according to the module docstring rules."""
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text.casefold():
        if _is_han(ch):
            if buf:
                tokens.append("".join(buf))
                buf = []
            tokens.append(ch)
        elif _is_word_char(ch):
            buf.append(ch)
        elif buf:
            tokens.append("".join(buf))
            buf = []
    if buf:
        tokens.append("".join(buf))
    return tokens


def token_set(text: str) -> set[str]:
    """Unique tokens of *text* (a document token is indexed only once)."""
    return set(tokenize(text))
