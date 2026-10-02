"""Token model with explicit identity for source vs. inserted tokens."""
from __future__ import annotations

import itertools
from dataclasses import dataclass

EOF_SYMBOL = "$"

SOURCE = "source"
INSERTED = "inserted"
SUBSTITUTED = "substituted"
EOF_ORIGIN = "eof"

_counters = {}


def _uid(prefix):
    counter = _counters.setdefault(prefix, itertools.count())
    return f"{prefix}-{next(counter)}"


@dataclass(frozen=True)
class Token:
    type: str
    value: str
    pos: int
    uid: str
    origin: str = SOURCE

    @property
    def is_source(self):
        return self.origin == SOURCE


def source_token(type_, value, pos):
    return Token(type_, value, pos, _uid("src"), SOURCE)


def inserted_token(type_, pos):
    return Token(type_, "", pos, _uid("ins"), INSERTED)


def substituted_token(type_, pos):
    return Token(type_, "", pos, _uid("sub"), SUBSTITUTED)


def eof_token(pos):
    return Token(EOF_SYMBOL, "", pos, _uid("eof"), EOF_ORIGIN)
