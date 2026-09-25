"""Token definitions. Every token keeps its identity and a UTF-8 byte span."""
from __future__ import annotations

import itertools
from dataclasses import dataclass

LET = "LET"
IDENT = "IDENT"
NUMBER = "NUMBER"
STRING = "STRING"
EQ = "EQ"
SEMI = "SEMI"
PLUS = "PLUS"
MINUS = "MINUS"
STAR = "STAR"
SLASH = "SLASH"
LPAREN = "LPAREN"
RPAREN = "RPAREN"
EOF = "EOF"

_virtual_ids = itertools.count(1)

_VIRTUAL_TEXT = {"NUMBER": "0", "STRING": '""', "IDENT": "_"}


@dataclass
class Token:
    kind: str
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int  # UTF-8 byte offset, exclusive
    token_id: int
    virtual: bool = False  # True for tokens fabricated by error recovery
    orig_index: int | None = None  # index in the original lexed stream


def virtual_token(kind, start, end=None, orig_index=None):
    """Create a virtual (recovery) token. Negative ids never collide with
    lexed token ids, so original token identity stays distinguishable."""
    return Token(kind, _VIRTUAL_TEXT.get(kind, ""),
                 start, start if end is None else end,
                 -next(_virtual_ids), True, orig_index)
