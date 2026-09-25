"""Hand-written lexer. All token positions are UTF-8 byte offsets."""

from dataclasses import dataclass

from .errors import LangError

KEYWORDS = {
    "fn", "let", "if", "else", "while", "return", "throw",
    "try", "catch", "finally", "yield",
    "true", "false", "null", "and", "or", "not",
}

TWO_CHAR_OPS = {"==", "!=", "<=", ">="}
SINGLE_CHAR_OPS = set("+-*/%<>=")
PUNCT = set("(){};,")


@dataclass
class Token:
    kind: str    # "int" | "string" | "ident" | "keyword" | "op" | "punct" | "eof"
    value: object
    start: int   # UTF-8 byte offset (inclusive)
    end: int     # UTF-8 byte offset (exclusive)


def _is_ident_start(ch):
    return ch.isalpha() or ch == "_" or ord(ch) > 127


def _is_ident_part(ch):
    return ch.isalnum() or ch == "_" or ord(ch) > 127


class Lexer:
    def __init__(self, source):
        self.source = source
        self.pos = 0        # index into the str
        self.byte_pos = 0   # UTF-8 byte offset of self.pos

    def tokenize(self):
        tokens = []
        src = self.source
        while self.pos < len(src):
            ch = src[self.pos]
            if ch.isspace():
                self._advance(ch)
                continue
            if ch == "/" and self._peek_char(1) == "/":
                while self.pos < len(src) and src[self.pos] != "\n":
                    self._advance(src[self.pos])
                continue
            start = self.byte_pos
            if ch.isdigit():
                text = self._take_while(str.isdigit)
                tokens.append(Token("int", int(text), start, self.byte_pos))
            elif ch == '"':
                tokens.append(self._string())
            elif _is_ident_start(ch):
                text = self._take_while(_is_ident_part)
                kind = "keyword" if text in KEYWORDS else "ident"
                tokens.append(Token(kind, text, start, self.byte_pos))
            elif ch in PUNCT:
                self._advance(ch)
                tokens.append(Token("punct", ch, start, self.byte_pos))
            elif ch in SINGLE_CHAR_OPS:
                nxt = self._peek_char(1)
                if ch + nxt in TWO_CHAR_OPS:
                    self._advance(ch)
                    self._advance(nxt)
                    tokens.append(Token("op", ch + nxt, start, self.byte_pos))
                else:
                    self._advance(ch)
                    tokens.append(Token("op", ch, start, self.byte_pos))
            else:
                end = start + len(ch.encode("utf-8"))
                raise LangError("unexpected character %r" % ch, (start, end))
        tokens.append(Token("eof", None, self.byte_pos, self.byte_pos))
        return tokens

    def _advance(self, ch):
        self.pos += 1
        self.byte_pos += len(ch.encode("utf-8"))

    def _peek_char(self, offset):
        idx = self.pos + offset
        return self.source[idx] if idx < len(self.source) else ""

    def _take_while(self, predicate):
        start = self.pos
        while self.pos < len(self.source) and predicate(self.source[self.pos]):
            self._advance(self.source[self.pos])
        return self.source[start:self.pos]

    def _string(self):
        start = self.byte_pos
        self._advance('"')
        out = []
        escapes = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\", "0": "\0"}
        while True:
            if self.pos >= len(self.source):
                raise LangError("unterminated string literal", (start, self.byte_pos))
            ch = self.source[self.pos]
            if ch == '"':
                self._advance(ch)
                return Token("string", "".join(out), start, self.byte_pos)
            if ch == "\\":
                self._advance(ch)
                if self.pos >= len(self.source):
                    raise LangError("unterminated string literal", (start, self.byte_pos))
                esc = self.source[self.pos]
                if esc not in escapes:
                    esc_start = self.byte_pos
                    esc_end = esc_start + len(esc.encode("utf-8"))
                    raise LangError("unknown escape \\%s" % esc, (esc_start, esc_end))
                out.append(escapes[esc])
                self._advance(esc)
            else:
                out.append(ch)
                self._advance(ch)
