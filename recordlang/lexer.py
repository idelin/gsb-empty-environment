"""Lexer producing tokens annotated with UTF-8 byte ranges."""

from __future__ import annotations

from dataclasses import dataclass

from .errors import LangError

KEYWORDS = {
    "let", "fn", "if", "then", "else", "with", "extend",
    "true", "false", "ro", "rw", "Int", "Bool",
}

_MULTI_CHAR_PUNCT = ("->", "==", "!=", "<=", ">=")
_SINGLE_CHAR_PUNCT = set("(){}[],:;.=@+-*/<>")


def utf8_byte_offsets(src: str) -> list[int]:
    """offsets[i] = number of UTF-8 bytes in src[:i]."""
    offsets = [0] * (len(src) + 1)
    total = 0
    for i, ch in enumerate(src):
        offsets[i] = total
        total += len(ch.encode("utf-8"))
    offsets[len(src)] = total
    return offsets


@dataclass(frozen=True)
class Token:
    kind: str  # "IDENT", "INT", "EOF", or a literal such as "let", "{", "->"
    text: str
    span: tuple[int, int]  # [start, end) as UTF-8 byte offsets


class Lexer:
    def __init__(self, src: str):
        self.src = src
        self.byte_offsets = utf8_byte_offsets(src)
        self.pos = 0

    def span_from(self, start_char: int) -> tuple[int, int]:
        return (self.byte_offsets[start_char], self.byte_offsets[self.pos])

    def tokenize(self) -> list[Token]:
        tokens: list[Token] = []
        while True:
            self._skip_trivia()
            if self.pos >= len(self.src):
                eof = self.byte_offsets[self.pos]
                tokens.append(Token("EOF", "", (eof, eof)))
                return tokens
            start = self.pos
            ch = self.src[self.pos]
            if ch.isdigit():
                while self.pos < len(self.src) and self.src[self.pos].isdigit():
                    self.pos += 1
                tokens.append(Token("INT", self.src[start:self.pos], self.span_from(start)))
            elif ch.isalpha() or ch == "_":
                while self.pos < len(self.src) and (
                    self.src[self.pos].isalnum() or self.src[self.pos] == "_"
                ):
                    self.pos += 1
                text = self.src[start:self.pos]
                kind = text if text in KEYWORDS else "IDENT"
                tokens.append(Token(kind, text, self.span_from(start)))
            else:
                matched = False
                for punct in _MULTI_CHAR_PUNCT:
                    if self.src.startswith(punct, self.pos):
                        self.pos += len(punct)
                        tokens.append(Token(punct, punct, self.span_from(start)))
                        matched = True
                        break
                if not matched:
                    if ch in _SINGLE_CHAR_PUNCT:
                        self.pos += 1
                        tokens.append(Token(ch, ch, self.span_from(start)))
                    else:
                        raise LangError(
                            f"unexpected character {ch!r}", self.span_from(start)
                        )

    def _skip_trivia(self) -> None:
        while self.pos < len(self.src):
            if self.src[self.pos].isspace():
                self.pos += 1
            elif self.src.startswith("//", self.pos):
                while self.pos < len(self.src) and self.src[self.pos] != "\n":
                    self.pos += 1
            else:
                return
