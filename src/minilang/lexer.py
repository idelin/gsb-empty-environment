from __future__ import annotations

from .common import SyntaxErr
from .nodes import Token

KEYWORDS = {"true", "false", "fun", "let", "in", "if", "then", "else"}
TWO_CHAR = {"->", "==", "!=", "<=", ">="}
ONE_CHAR = set("()+-*/<>=.;")


def is_ident_start(b: int) -> bool:
    return b == 95 or 65 <= b <= 90 or 97 <= b <= 122  # _ A-Z a-z


def is_ident_cont(b: int) -> bool:
    return is_ident_start(b) or 48 <= b <= 57  # + digits


class Lexer:
    def __init__(self, source_bytes: bytes):
        self.src = source_bytes
        self.n = len(source_bytes)
        self.i = 0

    def tokenize(self) -> list[Token]:
        tokens: list[Token] = []
        while self.i < self.n:
            b = self.src[self.i]
            if b in (32, 9, 10, 13):  # space, tab, newline, CR
                self.i += 1
                continue
            if b == 47 and self.i + 1 < self.n and self.src[self.i + 1] == 47:
                while self.i < self.n and self.src[self.i] != 10:
                    self.i += 1
                continue
            start = self.i
            if 48 <= b <= 57:
                while self.i < self.n and 48 <= self.src[self.i] <= 57:
                    self.i += 1
                tokens.append(Token("INT", self.src[start:self.i].decode(), start, self.i))
                continue
            # Identifiers begin with an ASCII letter/underscore or any
            # non-ASCII UTF-8 encoded character; positions remain byte offsets.
            if is_ident_start(b) or b >= 0x80:
                while self.i < self.n:
                    cb = self.src[self.i]
                    if is_ident_cont(cb) or cb >= 0x80:
                        self.i += 1
                        continue
                    break
                word = self.src[start:self.i].decode("utf-8")
                kind = word.upper() if word in KEYWORDS else "IDENT"
                tokens.append(Token(kind, word, start, self.i))
                continue
            if self.i + 1 < self.n:
                two = self.src[self.i:self.i + 2].decode("ascii", "ignore")
                if two in TWO_CHAR:
                    tokens.append(Token(two, two, start, start + 2))
                    self.i += 2
                    continue
            ch = chr(b)
            if ch in ONE_CHAR:
                tokens.append(Token(ch, ch, start, start + 1))
                self.i += 1
                continue
            raise SyntaxErr(f"unexpected byte 0x{b:02x} in source", start)
        tokens.append(Token("EOF", "", self.n, self.n))
        return tokens
