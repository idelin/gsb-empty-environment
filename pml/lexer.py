from __future__ import annotations

from dataclasses import dataclass

from .errors import CompileError, Span, render_diagnostic


KEYWORDS = {"union", "let", "match", "true", "false", "if", "then", "else"}


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int
    end: int

    @property
    def span(self) -> Span:
        return Span(self.start, self.end)


def is_ident_start(char: str) -> bool:
    return char.isalpha() or char == "_"


def is_ident_part(char: str) -> bool:
    return char.isalnum() or char == "_"


class Lexer:
    def __init__(self, source: bytes, filename: str = "<input>"):
        self.source = source
        self.text = source.decode("utf-8")
        self.filename = filename
        self.index = 0
        self.byte_pos = 0

    def tokenize(self) -> list[Token]:
        tokens: list[Token] = []
        while self.index < len(self.text):
            char = self.text[self.index]
            if char in " \t\r\n":
                self.advance()
            elif self.index + 1 < len(self.text) and self.text[self.index : self.index + 2] == "//":
                self.skip_comment()
            elif is_ident_start(char):
                tokens.append(self.read_identifier())
            elif char.isdigit():
                tokens.append(self.read_number())
            elif char == '"':
                tokens.append(self.read_string())
            else:
                token = self.read_operator()
                if token is not None:
                    tokens.append(token)
                else:
                    start = self.byte_pos
                    self.advance()
                    span = Span(start, self.byte_pos)
                    raise CompileError(
                        [render_diagnostic(self.source, span, f"unexpected character {char!r}", self.filename)]
                    )
        tokens.append(Token("EOF", "", self.byte_pos, self.byte_pos))
        return tokens

    def advance(self) -> str:
        char = self.text[self.index]
        self.index += 1
        self.byte_pos += len(char.encode("utf-8"))
        return char

    def skip_comment(self) -> None:
        while self.index < len(self.text) and self.text[self.index] != "\n":
            self.advance()

    def read_identifier(self) -> Token:
        start = self.byte_pos
        char_start = self.index
        while self.index < len(self.text) and is_ident_part(self.text[self.index]):
            self.advance()
        text = self.text[char_start : self.index]
        kind = "KEYWORD" if text in KEYWORDS else "IDENT"
        return Token(kind, text, start, self.byte_pos)

    def read_number(self) -> Token:
        start = self.byte_pos
        char_start = self.index
        while self.index < len(self.text) and self.text[self.index].isdigit():
            self.advance()
        if self.index < len(self.text) and self.text[self.index] == ".":
            span = Span(start, self.byte_pos + 1)
            raise CompileError([render_diagnostic(self.source, span, "floating-point numbers are not supported", self.filename)])
        return Token("INT", self.text[char_start : self.index], start, self.byte_pos)

    def read_string(self) -> Token:
        start = self.byte_pos
        self.advance()
        chars: list[str] = []
        while self.index < len(self.text) and self.text[self.index] != '"':
            char = self.text[self.index]
            if char == "\n":
                span = Span(self.byte_pos, self.byte_pos + 1)
                raise CompileError([render_diagnostic(self.source, span, "unterminated string", self.filename)])
            if char == "\\":
                self.advance()
                if self.index >= len(self.text):
                    break
                escaped = self.text[self.index]
                mapping = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}
                if escaped not in mapping:
                    span = Span(self.byte_pos, self.byte_pos + len(escaped.encode("utf-8")))
                    raise CompileError(
                        [render_diagnostic(self.source, span, f"invalid escape \\{escaped}", self.filename)]
                    )
                chars.append(mapping[escaped])
                self.advance()
            else:
                chars.append(char)
                self.advance()
        if self.index >= len(self.text):
            span = Span(start, self.byte_pos)
            raise CompileError([render_diagnostic(self.source, span, "unterminated string", self.filename)])
        self.advance()
        return Token("STRING", "".join(chars), start, self.byte_pos)

    def read_operator(self) -> Token | None:
        start = self.byte_pos
        two = self.text[self.index : self.index + 2]
        two_char = {
            "==",
            "!=",
            "<=",
            ">=",
            "&&",
            "||",
            "->",
        }
        if two in two_char:
            self.advance()
            self.advance()
            return Token(two, two, start, self.byte_pos)
        one = self.text[self.index]
        if one in "(){}[],:;=+-*/<>!.|":
            self.advance()
            return Token(one, one, start, self.byte_pos)
        return None
