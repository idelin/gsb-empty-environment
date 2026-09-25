"""Lexer. All token ranges are UTF-8 *byte* offsets into the source bytes."""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from typing import List, Tuple

ALL_KINDS: Tuple[str, ...] = (
    "let", "name", "=", "int", "string", "+", "(", ")", ";",
)

CANONICAL_TEXT = {
    "let": "let",
    "name": "<name>",
    "=": "=",
    "int": "0",
    "string": '""',
    "+": "+",
    "(": "(",
    ")": ")",
    ";": ";",
}

_id_counter = itertools.count(1)


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int          # UTF-8 byte offset, inclusive
    end: int            # UTF-8 byte offset, exclusive
    id: int
    virtual: bool = False        # True for tokens synthesized by recovery
    replaces: "Token | None" = None  # original token a virtual token replaces

    def __repr__(self) -> str:
        tag = "*" if self.virtual else ""
        return f"Token({self.kind!r}{tag}, {self.text!r}, {self.start}:{self.end})"


@dataclass
class Diagnostic:
    message: str
    start: int          # UTF-8 byte offset, inclusive
    end: int            # UTF-8 byte offset, exclusive
    severity: str = "error"       # "error" or "runtime"
    minimal: bool = False         # True only for a proven minimal-edit repair
    candidates: Tuple[str, ...] = ()  # all equal-cost repair descriptions


def make_token(kind: str, text: str, start: int, end: int) -> Token:
    return Token(kind, text, start, end, next(_id_counter))


def make_virtual(kind: str, pos: int, replaces: "Token | None" = None) -> Token:
    """A synthesized token. Replacement keeps the original token's byte range;
    an insertion is zero-width at `pos`."""
    if replaces is not None:
        return Token(
            kind, CANONICAL_TEXT[kind],
            replaces.start, replaces.end,
            next(_id_counter), virtual=True, replaces=replaces,
        )
    return Token(
        kind, CANONICAL_TEXT[kind],
        pos, pos, next(_id_counter), virtual=True,
    )


_SINGLE = {"=": "=", "+": "+", "(": "(", ")": ")", ";": ";"}


def _is_ident_start(ch: str) -> bool:
    return ch == "_" or ch.isalpha()


def _is_ident_part(ch: str) -> bool:
    return ch == "_" or ch.isalnum()


def _is_structural(ch: str) -> bool:
    return ch in _SINGLE or ch == '"' or ch.isspace() or _is_ident_part(ch)


def lex(text: str) -> Tuple[List[Token], List[Diagnostic]]:
    tokens: List[Token] = []
    diags: List[Diagnostic] = []

    i = 0
    byte = 0
    n = len(text)

    while i < n:
        ch = text[i]

        if ch.isspace():
            byte += len(ch.encode("utf-8"))
            i += 1
            continue

        # line comment
        if ch == "/" and i + 1 < n and text[i + 1] == "/":
            while i < n and text[i] != "\n":
                byte += len(text[i].encode("utf-8"))
                i += 1
            continue

        start = byte

        if ch in _SINGLE:
            tokens.append(make_token(_SINGLE[ch], ch, start, start + 1))
            byte += 1
            i += 1
            continue

        if ch.isdigit():
            j = i
            while j < n and text[j].isdigit():
                j += 1
            s = text[i:j]
            b = len(s.encode("utf-8"))
            tokens.append(make_token("int", s, start, start + b))
            byte += b
            i = j
            continue

        if ch == '"':
            # An unterminated string is exactly ONE error and ONE token: it
            # runs only to end of line / EOF, never across the whole file.
            j = i + 1
            closed = False
            while j < n:
                if text[j] == '"':
                    closed = True
                    j += 1
                    break
                if text[j] == "\n":
                    break
                j += 1
            s = text[i:j]
            b = len(s.encode("utf-8"))
            tokens.append(make_token("string", s, start, start + b))
            if not closed:
                diags.append(
                    Diagnostic("unterminated string literal", start, start + b)
                )
            byte += b
            i = j
            continue

        if _is_ident_start(ch):
            j = i
            while j < n and _is_ident_part(text[j]):
                j += 1
            s = text[i:j]
            b = len(s.encode("utf-8"))
            kind = "let" if s == "let" else "name"
            tokens.append(make_token(kind, s, start, start + b))
            byte += b
            i = j
            continue

        # Run of invalid characters: one grouped diagnostic, skip the run.
        j = i
        while j < n and not _is_structural(text[j]):
            j += 1
        s = text[i:j]
        b = len(s.encode("utf-8"))
        diags.append(
            Diagnostic(f"unexpected character(s) {s!r}", start, start + b)
        )
        byte += b
        i = j

    return tokens, diags
