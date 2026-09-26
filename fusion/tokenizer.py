"""Tokenization rules (fixed and public, mirrored in README.md):

1. Input is a ``str`` (already UTF-8 decoded).
2. A maximal run of ASCII letters/digits (A-Z, a-z, 0-9) forms one token,
   lowercased.  ``_`` and every other non-alnum ASCII char is a separator.
3. Every CJK Unified Ideograph (U+4E00..U+9FFF) forms a single-character
   token (unigram).
4. Every other character (whitespace, punctuation, symbols, other scripts
   such as Latin-1 accented letters) is a separator and yields no token.

The very same tokenizer is applied to documents and to queries, so a query
term can always match the corresponding document tokens.

Example: ``"Hello, 世界! ML_2024"`` -> ``["hello", "世", "界", "ml", "2024"]``
"""

from __future__ import annotations


def _is_ascii_alnum(ch: str) -> bool:
    return ch.isascii() and ch.isalnum()


def _is_cjk(ch: str) -> bool:
    return "\u4e00" <= ch <= "\u9fff"


def tokenize(text: str) -> list[str]:
    """Split *text* into tokens according to the rules in the module docstring."""
    if not isinstance(text, str):
        raise TypeError("tokenize() expects a str")
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text:
        if _is_ascii_alnum(ch):
            buf.append(ch.lower())
        else:
            if buf:
                tokens.append("".join(buf))
                buf = []
            if _is_cjk(ch):
                tokens.append(ch)
    if buf:
        tokens.append("".join(buf))
    return tokens
