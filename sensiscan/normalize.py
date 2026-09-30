"""Normalization paths applied before dictionary matching.

Two paths are provided so that different kinds of obfuscation are caught:

- ``nfkc_casefold``: Unicode NFKC normalization + case folding.  Catches
  full-width characters, compatibility variants and case changes.
- ``nfkc_casefold_desep``: additionally drops separator and invisible
  characters (spaces, dashes, dots, zero-width characters, soft hyphens).
  Catches values broken up by separators.

Every normalized character keeps the raw source span of the character it
was derived from, so a match in normalized text can be mapped back to the
original text.
"""
from __future__ import annotations

import unicodedata

SEPARATORS = frozenset(" \t\r\n-_.·•\u00ad\u200b\u200c\u200d\ufeff")

NORM_PATHS = ("nfkc_casefold", "nfkc_casefold_desep")


def normalize_text(
    mode: str, text: str, spans: list[tuple[int, int]]
) -> tuple[str, list[tuple[int, int]]]:
    if mode not in NORM_PATHS:
        raise ValueError(f"unknown normalization path: {mode}")
    drop = mode == "nfkc_casefold_desep"
    out_chars: list[str] = []
    out_spans: list[tuple[int, int]] = []
    for ch, sp in zip(text, spans):
        for nch in unicodedata.normalize("NFKC", ch).casefold():
            if drop and nch in SEPARATORS:
                continue
            out_chars.append(nch)
            out_spans.append(sp)
    return "".join(out_chars), out_spans


def normalize_entry(mode: str, entry: str) -> str:
    """Normalize a dictionary entry through one path (spans discarded)."""
    spans = [(i, i + 1) for i in range(len(entry))]
    return normalize_text(mode, entry, spans)[0]
