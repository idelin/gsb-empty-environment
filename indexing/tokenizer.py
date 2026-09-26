"""Deterministic tokenizer for locally synthesized UTF-8 text.

Tokenization rules (the full spec lives in README.md):

1. Input is ``str``; ``bytes`` are decoded as UTF-8 (strict, errors raise).
2. ASCII letters and digits form maximal runs ``[A-Za-z0-9]+`` and are
   lowercased (``Hello`` -> ``hello``).
3. Every CJK unified ideograph is a single-character token (unigram).
   Covered ranges: U+3400-U+4DBF, U+4E00-U+9FFF, U+F900-U+FAFF.
4. Every other character (whitespace, punctuation, symbols, underscores)
   is a separator and produces no token.
"""
from __future__ import annotations

import re

_CJK = "\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff"
_TOKEN_RE = re.compile(r"[A-Za-z0-9]+|[" + _CJK + "]")


def tokenize(text: str | bytes) -> list[str]:
    """Split text into a list of tokens according to the rules above."""
    if isinstance(text, bytes):
        text = text.decode("utf-8")  # strict: invalid UTF-8 raises
    if not isinstance(text, str):
        raise TypeError(f"expected str or bytes, got {type(text).__name__}")
    return [m.group(0).lower() for m in _TOKEN_RE.finditer(text)]


def term_counts(text: str | bytes) -> dict[str, int]:
    """Return {term: frequency} for the text. Pure function of the input."""
    counts: dict[str, int] = {}
    for token in tokenize(text):
        counts[token] = counts.get(token, 0) + 1
    return counts
