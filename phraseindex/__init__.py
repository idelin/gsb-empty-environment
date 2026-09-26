"""Positional inverted index with phrase / gapped-phrase / synonym queries.

Pure Python 3.11 standard library. No search service, no full-text library.
"""

from .tokenizer import Token, tokenize
from .postings import BlockedPositions, DecodeCounter, BLOCK_SIZE
from .query import PhraseQuery
from .index import Index, Snapshot, Segment, Match, Evidence
from .verify import VerifyReport, verify_snapshot, brute_force_field

__all__ = [
    "Token",
    "tokenize",
    "BlockedPositions",
    "DecodeCounter",
    "BLOCK_SIZE",
    "PhraseQuery",
    "Index",
    "Snapshot",
    "Segment",
    "Match",
    "Evidence",
    "VerifyReport",
    "verify_snapshot",
    "brute_force_field",
]
