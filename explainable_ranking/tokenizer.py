"""Deterministic tokenizer shared by the ranker and the verifier.

Tokenization rules (stable contract, do not change silently):

1. Normalize the input with Unicode NFKC.
2. Apply Unicode casefolding.
3. Scan left to right and emit tokens:
   - a maximal run of ASCII letters/digits (``[a-z0-9]+`` after
     casefolding) is emitted as one token;
   - every CJK character (Han, Hiragana, Katakana, Hangul) is emitted
     as a token of its own (character-level segmentation);
   - every other character (whitespace, punctuation, symbols) is a
     separator and produces no token.
"""
from __future__ import annotations

import unicodedata


def _is_cjk(char: str) -> bool:
    cp = ord(char)
    return (
        0x4E00 <= cp <= 0x9FFF      # CJK Unified Ideographs
        or 0x3400 <= cp <= 0x4DBF   # CJK Extension A
        or 0xF900 <= cp <= 0xFAFF   # CJK Compatibility Ideographs
        or 0x3040 <= cp <= 0x30FF   # Hiragana + Katakana
        or 0xAC00 <= cp <= 0xD7AF   # Hangul syllables
    )


def tokenize(text: str) -> list[str]:
    tokens: list[str] = []
    buf: list[str] = []
    for char in unicodedata.normalize("NFKC", text).casefold():
        if char.isascii() and char.isalnum():
            buf.append(char)
            continue
        if buf:
            tokens.append("".join(buf))
            buf = []
        if _is_cjk(char):
            tokens.append(char)
    if buf:
        tokens.append("".join(buf))
    return tokens
