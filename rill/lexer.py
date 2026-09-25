"""Lexer for Rill.

All spans are half-open UTF-8 *byte* intervals ``[start, end)`` into the
encoded source text.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import CompileError

KEYWORDS = {
    "fn", "let", "var", "if", "else", "while", "return",
    "readonly", "writable", "true", "false", "extend",
}
TWO_CHAR = {"==", "!=", "<=", ">=", "&&", "||"}
SINGLE_CHAR = set("+-*/%<>=!(){}[],:;.")


@dataclass
class Token:
    kind: str
    value: str
    start: int
    end: int
    line: int


class Lexer:
    def __init__(self, source: str):
        self.src = source
        self.data = source.encode("utf-8")
        self.pos = 0  # character index
        self.line = 1

    def byte_pos(self, char_pos: int | None = None) -> int:
        return len(self.src[: char_pos if char_pos is not None else self.pos].encode("utf-8"))

    def error(self, msg: str, start_char: int, end_char: int) -> CompileError:
        return CompileError(msg, len(self.src[:start_char].encode("utf-8")),
                            len(self.src[:end_char].encode("utf-8")))

    def tokenize(self) -> list[Token]:
        tokens: list[Token] = []
        s = self.src
        n = len(s)
        while self.pos < n:
            ch = s[self.pos]
            if ch in " \t\r":
                self.pos += 1
                continue
            if ch == "\n":
                self.line += 1
                self.pos += 1
                continue
            if ch == "#":
                while self.pos < n and s[self.pos] != "\n":
                    self.pos += 1
                continue
            start = self.pos
            bs = self.byte_pos()
            if ch.isalpha() or ch == "_":
                while self.pos < n and (s[self.pos].isalnum() or s[self.pos] == "_"):
                    self.pos += 1
                word = s[start:self.pos]
                kind = word if word in KEYWORDS else "NAME"
                tokens.append(Token(kind, word, bs, self.byte_pos(), self.line))
                continue
            if ch.isdigit():
                while self.pos < n and s[self.pos].isdigit():
                    self.pos += 1
                word = s[start:self.pos]
                tokens.append(Token("INT", word, bs, self.byte_pos(), self.line))
                continue
            if ch == '"':
                tokens.append(self.lex_string())
                continue
            if self.pos + 1 < n and s[self.pos:self.pos + 2] in TWO_CHAR:
                op = s[self.pos:self.pos + 2]
                self.pos += 2
                tokens.append(Token(op, op, bs, self.byte_pos(), self.line))
                continue
            if ch in SINGLE_CHAR:
                self.pos += 1
                tokens.append(Token(ch, ch, bs, self.byte_pos(), self.line))
                continue
            raise self.error(f"unexpected character {ch!r}", start, start + 1)
        tokens.append(Token("EOF", "", len(self.data), len(self.data), self.line))
        return tokens

    def lex_string(self) -> Token:
        s = self.src
        start = self.pos
        bs = self.byte_pos()
        self.pos += 1  # opening quote
        out: list[str] = []
        escapes = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}
        while self.pos < len(s) and s[self.pos] != '"':
            ch = s[self.pos]
            if ch == "\n":
                raise self.error("unterminated string literal", start, self.pos)
            if ch == "\\":
                self.pos += 1
                if self.pos >= len(s):
                    break
                e = s[self.pos]
                if e not in escapes:
                    raise self.error(f"invalid string escape \\{e}", self.pos - 1, self.pos + 1)
                out.append(escapes[e])
                self.pos += 1
            else:
                out.append(ch)
                self.pos += 1
        if self.pos >= len(s):
            raise self.error("unterminated string literal", start, self.pos)
        self.pos += 1  # closing quote
        return Token("STR", "".join(out), bs, self.byte_pos(), self.line)
