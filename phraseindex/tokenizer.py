"""Tokenizer for local synthetic UTF-8 text.

Tokenization rules (documented contract):
  1. Input is a Python ``str`` decoded from UTF-8 (or built in memory).
  2. A token is a *maximal run* of Unicode alphanumeric code points
     (``str.isalnum()`` is True). Everything else (whitespace, punctuation,
     emoji, CJK punctuation, ...) is a separator and is dropped.
  3. Tokens are normalized with ``str.casefold()`` so matching is
     case-insensitive (e.g. "Cafe" and "CAFE" share a posting list).
  4. Offsets ``start``/``end`` are code-point indices into the *original*
     (un-normalized) string: ``text[start:end]`` is the surface form.
  5. Positions are per (document, field): the first token of every field
     gets position 0. Phrases never span fields.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Token:
    term: str   # casefolded surface form (the indexed term)
    start: int  # code-point offset, inclusive
    end: int    # code-point offset, exclusive


def tokenize(text: str) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    n = len(text)
    while i < n:
        if text[i].isalnum():
            j = i + 1
            while j < n and text[j].isalnum():
                j += 1
            tokens.append(Token(text[i:j].casefold(), i, j))
            i = j
        else:
            i += 1
    return tokens
