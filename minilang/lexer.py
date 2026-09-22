"""Hand-written lexer.

Every token carries a ``(start, end)`` span expressed as UTF-8 *byte*
offsets into the source (``start`` inclusive, ``end`` exclusive), not
character indices.  A char-index -> byte-offset table is precomputed so
spans stay correct even when the source contains multi-byte characters.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any

KEYWORDS = {
    "let": "LET",
    "fn": "FN",
    "if": "IF",
    "else": "ELSE",
    "while": "WHILE",
    "for": "FOR",
    "break": "BREAK",
    "continue": "CONTINUE",
    "return": "RETURN",
    "throw": "THROW",
    "try": "TRY",
    "catch": "CATCH",
    "finally": "FINALLY",
    "true": "TRUE",
    "false": "FALSE",
    "nil": "NIL",
}

SINGLE_CHAR = {
    "(": "LEFT_PAREN",
    ")": "RIGHT_PAREN",
    "{": "LEFT_BRACE",
    "}": "RIGHT_BRACE",
    ";": "SEMICOLON",
    ",": "COMMA",
    "+": "PLUS",
    "-": "MINUS",
    "*": "STAR",
    "/": "SLASH",
    "%": "PERCENT",
}

ESCAPES = {
    "n": "\n",
    "t": "\t",
    "r": "\r",
    "0": "\0",
    "\"": "\"",
    "\\": "\\",
}


@dataclass
class Token:
    kind: str
    lexeme: str
    value: Any
    start: int  # UTF-8 byte offset, inclusive
    end: int  # UTF-8 byte offset, exclusive

    def __repr__(self):  # pragma: no cover - debugging aid
        return f"Token({self.kind}, {self.lexeme!r}, bytes {self.start}..{self.end})"


class LexError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(f"{message} (bytes {start}..{end})")
        self.message = message
        self.start = start
        self.end = end


class Lexer:
    def __init__(self, source: str):
        self.source = source
        self.pos = 0  # current *character* index
        self.length = len(source)
        # char index -> UTF-8 byte offset
        offsets = [0]
        acc = 0
        for ch in source:
            acc += len(ch.encode("utf-8"))
            offsets.append(acc)
        self._byte_offsets = offsets
        self.tokens: list[Token] = []

    # -- public ---------------------------------------------------------

    def tokenize(self) -> list[Token]:
        while not self._at_end():
            self._scan_token()
        eof = self._byte_offsets[self.length]
        self.tokens.append(Token("EOF", "", None, eof, eof))
        return self.tokens

    # -- helpers --------------------------------------------------------

    def _byte(self, char_index: int) -> int:
        return self._byte_offsets[char_index]

    def _at_end(self) -> bool:
        return self.pos >= self.length

    def _peek(self, ahead: int = 0) -> str:
        index = self.pos + ahead
        return self.source[index] if index < self.length else ""

    def _emit(self, kind: str, lexeme: str, value: Any, char_start: int, char_end: int):
        self.tokens.append(
            Token(kind, lexeme, value, self._byte(char_start), self._byte(char_end))
        )

    # -- scanning -------------------------------------------------------

    def _scan_token(self):
        ch = self._peek()
        if ch in " \t\r\n":
            self.pos += 1
            return
        if ch == "/" and self._peek(1) == "/":
            while not self._at_end() and self._peek() != "\n":
                self.pos += 1
            return
        if ch == "/" and self._peek(1) == "*":
            self._skip_block_comment()
            return
        if ch in SINGLE_CHAR:
            self._emit(SINGLE_CHAR[ch], ch, None, self.pos, self.pos + 1)
            self.pos += 1
            return
        if ch in "=!<>":
            self._scan_operator()
            return
        if ch == "&":
            self._expect_pair("&&", "AND")
            return
        if ch == "|":
            self._expect_pair("||", "OR")
            return
        if ch == "\"":
            self._scan_string()
            return
        if ch.isdigit():
            self._scan_number()
            return
        if ch.isalpha() or ch == "_":
            self._scan_identifier()
            return
        raise LexError(
            f"unexpected character {ch!r}", self._byte(self.pos), self._byte(self.pos + 1)
        )

    def _skip_block_comment(self):
        start = self.pos
        self.pos += 2
        while True:
            if self._at_end():
                raise LexError(
                    "unterminated block comment", self._byte(start), self._byte(self.pos)
                )
            if self._peek() == "*" and self._peek(1) == "/":
                self.pos += 2
                return
            self.pos += 1

    def _scan_operator(self):
        start = self.pos
        ch = self.source[self.pos]
        self.pos += 1
        if self._peek() == "=":
            self.pos += 1
            kind = {
                "=": "EQUAL_EQUAL",
                "!": "BANG_EQUAL",
                "<": "LESS_EQUAL",
                ">": "GREATER_EQUAL",
            }[ch]
            self._emit(kind, ch + "=", None, start, self.pos)
        else:
            kind = {"=": "EQUAL", "!": "BANG", "<": "LESS", ">": "GREATER"}[ch]
            self._emit(kind, ch, None, start, self.pos)

    def _expect_pair(self, pair: str, kind: str):
        start = self.pos
        if self._peek(1) == pair[1]:
            self.pos += 2
            self._emit(kind, pair, None, start, self.pos)
        else:
            raise LexError(
                f"expected {pair!r} (did you mean {pair!r}?)",
                self._byte(start),
                self._byte(start + 1),
            )

    def _scan_string(self):
        start = self.pos
        self.pos += 1  # opening quote
        chars: list[str] = []
        while True:
            if self._at_end():
                raise LexError(
                    "unterminated string", self._byte(start), self._byte(self.pos)
                )
            ch = self.source[self.pos]
            self.pos += 1
            if ch == "\"":
                break
            if ch == "\\":
                if self._at_end():
                    raise LexError(
                        "unterminated string", self._byte(start), self._byte(self.pos)
                    )
                esc = self.source[self.pos]
                self.pos += 1
                if esc not in ESCAPES:
                    raise LexError(
                        f"invalid escape sequence \{esc}",
                        self._byte(self.pos - 2),
                        self._byte(self.pos),
                    )
                chars.append(ESCAPES[esc])
            else:
                chars.append(ch)
        lexeme = self.source[start:self.pos]
        self._emit("STRING", lexeme, "".join(chars), start, self.pos)

    def _scan_number(self):
        start = self.pos
        while self._peek().isdigit():
            self.pos += 1
        is_float = False
        if self._peek() == "." and self._peek(1).isdigit():
            is_float = True
            self.pos += 1
            while self._peek().isdigit():
                self.pos += 1
        text = self.source[start:self.pos]
        value = float(text) if is_float else int(text)
        self._emit("NUMBER", text, value, start, self.pos)

    def _scan_identifier(self):
        start = self.pos
        while True:
            ch = self._peek()
            if ch.isalnum() or ch == "_":
                self.pos += 1
            else:
                break
        text = self.source[start:self.pos]
        kind = KEYWORDS.get(text, "IDENT")
        self._emit(kind, text, None, start, self.pos)
