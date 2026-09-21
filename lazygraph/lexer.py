"""词法分析器。

直接扫描 UTF-8 字节序列：标识符/数字都是 ASCII，多字节字符只会出现在
注释里，因此按字节推进即能得到正确的字节偏移，无需 decode 整个源文件。
"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import LexError
from .sourcemap import Span

KEYWORDS = {
    "let",
    "rec",
    "in",
    "and",
    "if",
    "then",
    "else",
    "true",
    "false",
    "tick",
}

TWO_CHAR_OPS = {"==", "!=", "<=", ">=", "&&", "||"}
ONE_CHAR_OPS = set("+-*/%<>=(),")


@dataclass(frozen=True)
class Token:
    kind: str          # KEYWORD / INT / IDENT / OP / EOF
    value: str
    span: Span


class Lexer:
    def __init__(self, data: bytes) -> None:
        self.data = data
        self.pos = 0

    def _peek(self, offset: int = 0) -> int:
        index = self.pos + offset
        return self.data[index] if index < len(self.data) else -1

    def _skip_ws_and_comments(self) -> None:
        while self.pos < len(self.data):
            byte = self.data[self.pos]
            if byte in (0x20, 0x09, 0x0D, 0x0A):  # 空格、制表、CR、LF
                self.pos += 1
            elif byte == ord("#"):
                while self.pos < len(self.data) and self.data[self.pos] != 0x0A:
                    self.pos += 1
            else:
                break

    def tokenize(self) -> list[Token]:
        tokens: list[Token] = []
        while True:
            self._skip_ws_and_comments()
            if self.pos >= len(self.data):
                tokens.append(Token("EOF", "", Span(self.pos, self.pos)))
                return tokens

            start = self.pos
            byte = self.data[self.pos]

            if 0x30 <= byte <= 0x39:  # 数字
                while self.pos < len(self.data) and 0x30 <= self.data[self.pos] <= 0x39:
                    self.pos += 1
                value = self.data[start:self.pos].decode("ascii")
                tokens.append(Token("INT", value, Span(start, self.pos)))
                continue

            if (
                0x41 <= byte <= 0x5A
                or 0x61 <= byte <= 0x7A
                or byte == ord("_")
            ):  # 标识符/关键字
                while self.pos < len(self.data):
                    current = self.data[self.pos]
                    if (
                        0x41 <= current <= 0x5A
                        or 0x61 <= current <= 0x7A
                        or 0x30 <= current <= 0x39
                        or current == ord("_")
                    ):
                        self.pos += 1
                    else:
                        break
                value = self.data[start:self.pos].decode("ascii")
                kind = "KEYWORD" if value in KEYWORDS else "IDENT"
                tokens.append(Token(kind, value, Span(start, self.pos)))
                continue

            two = self.data[self.pos:self.pos + 2].decode("ascii", "ignore")
            if two in TWO_CHAR_OPS:
                self.pos += 2
                tokens.append(Token("OP", two, Span(start, self.pos)))
                continue

            if chr(byte) in ONE_CHAR_OPS:
                self.pos += 1
                tokens.append(Token("OP", chr(byte), Span(start, self.pos)))
                continue

            raise LexError(
                f"无法识别的字符 {self.data[start:start + 1]!r}（字节 {start}）",
                Span(start, start + 1),
            )


def tokenize(source: str | bytes) -> list[Token]:
    data = source.encode("utf-8") if isinstance(source, str) else source
    return Lexer(data).tokenize()
