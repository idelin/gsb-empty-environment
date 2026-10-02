"""Token model with explicit identity and source mapping.

Every token carries a unique ``uid`` so that original tokens and tokens
created by error recovery (inserted / substituted) have distinct identities
even when their text and kind coincide.  Source spans are preserved through
recovery: inserted tokens map to a zero-width span at the insertion point.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from enum import Enum

EOF_KIND = "$"

_uid_source = itertools.count(1)


class Origin(Enum):
    ORIGINAL = "original"
    INSERTED = "inserted"
    SUBSTITUTED = "substituted"
    EOF = "eof"


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int
    end: int
    origin: Origin = Origin.ORIGINAL
    uid: int = field(default_factory=lambda: next(_uid_source), compare=False)

    @property
    def span(self) -> tuple[int, int]:
        return (self.start, self.end)


def make_token(kind: str, text: str, start: int, end: int) -> Token:
    return Token(kind=kind, text=text, start=start, end=end, origin=Origin.ORIGINAL)


def inserted_token(kind: str, at: int) -> Token:
    return Token(kind=kind, text=kind, start=at, end=at, origin=Origin.INSERTED)


def substituted_token(kind: str, original: Token) -> Token:
    return Token(
        kind=kind,
        text=kind,
        start=original.start,
        end=original.end,
        origin=Origin.SUBSTITUTED,
    )


def eof_token(at: int) -> Token:
    return Token(kind=EOF_KIND, text="", start=at, end=at, origin=Origin.EOF)
