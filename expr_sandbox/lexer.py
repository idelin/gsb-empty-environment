"""Tokenizer.  All positions are UTF-8 byte offsets into the source."""

import math

from .budget import MAX_INT
from .errors import ParseError

KEYWORDS = frozenset({"and", "or", "not", "true", "false", "null", "if"})

_MULTI_OPS = ("//", "==", "!=", "<=", ">=")
_SINGLE_OPS = "+-*/%<>"
_PUNCT = {"(": "lparen", ")": "rparen", "[": "lbracket", "]": "rbracket",
          ",": "comma"}
_SIMPLE_ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "\\": "\\",
                   '"': '"', "0": "\0"}
_HEX = set("0123456789abcdefABCDEF")


def _is_digit(ch):
    return "0" <= ch <= "9"


class Token:
    __slots__ = ("kind", "value", "start", "end")

    def __init__(self, kind, value, start, end):
        self.kind = kind
        self.value = value
        self.start = start
        self.end = end

    def __repr__(self):
        return "Token(%r, %r, %d, %d)" % (self.kind, self.value,
                                          self.start, self.end)


def tokenize(source, budget, limits):
    if not isinstance(source, str):
        raise TypeError("source must be a string")
    if len(source.encode("utf-8")) > limits.max_source:
        raise ParseError("source too large", 0, limits.max_source)
    tokens = []
    i = 0          # index into source (characters)
    byte = 0       # UTF-8 byte offset of source[i]
    n = len(source)
    while i < n:
        ch = source[i]
        if ch in " \t\r\n":
            i += 1
            byte += 1
            continue
        start = byte
        if _is_digit(ch):
            i, byte, tok = _lex_number(source, i, byte, limits)
            tokens.append(tok)
            budget.consume(1, "parsing")
            continue
        if ch == '"':
            i, byte, tok = _lex_string(source, i, byte, limits)
            tokens.append(tok)
            budget.consume(1, "parsing")
            continue
        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (source[j].isalnum() or source[j] == "_"):
                j += 1
            text = source[i:j]
            end = start + len(text.encode("utf-8"))
            kind = "keyword" if text in KEYWORDS else "ident"
            tokens.append(Token(kind, text, start, end))
            budget.consume(1, "parsing")
            i, byte = j, end
            continue
        two = source[i:i + 2]
        if two in _MULTI_OPS:
            tokens.append(Token("op", two, start, start + 2))
            budget.consume(1, "parsing")
            i += 2
            byte += 2
            continue
        if ch in _SINGLE_OPS:
            tokens.append(Token("op", ch, start, start + 1))
            budget.consume(1, "parsing")
            i += 1
            byte += 1
            continue
        if ch in _PUNCT:
            tokens.append(Token(_PUNCT[ch], ch, start, start + 1))
            budget.consume(1, "parsing")
            i += 1
            byte += 1
            continue
        raise ParseError("unexpected character %r" % ch,
                         start, start + len(ch.encode("utf-8")))
    tokens.append(Token("eof", None, byte, byte))
    return tokens


def _lex_number(source, i, byte, limits):
    n = len(source)
    j = i
    while j < n and _is_digit(source[j]):
        j += 1
    int_digits = j - i
    frac_digits = 0
    is_float = False
    if j + 1 < n and source[j] == "." and _is_digit(source[j + 1]):
        is_float = True
        k = j + 1
        while k < n and _is_digit(source[k]):
            k += 1
        frac_digits = k - (j + 1)
        j = k
    if j < n and source[j] in "eE":
        k = j + 1
        if k < n and source[k] in "+-":
            k += 1
        if k < n and _is_digit(source[k]):
            is_float = True
            while k < n and _is_digit(source[k]):
                k += 1
            j = k
    text = source[i:j]
    end = byte + (j - i)  # number characters are ASCII
    if int_digits + frac_digits > limits.max_number_digits:
        raise ParseError("number literal too large", byte, end)
    if is_float:
        value = float(text)
        if not math.isfinite(value):
            raise ParseError("number out of range", byte, end)
    else:
        value = int(text)
        if value > MAX_INT:
            raise ParseError("number out of range", byte, end)
    return j, end, Token("number", value, byte, end)


def _lex_string(source, i, byte, limits):
    n = len(source)
    j = i + 1
    jbyte = byte + 1
    chars = []
    while j < n:
        ch = source[j]
        if ch == '"':
            value = "".join(chars)
            end = jbyte + 1
            return j + 1, end, Token("string", value, byte, end)
        if ch == "\\":
            if j + 1 >= n:
                raise ParseError("unterminated string", byte, jbyte + 1)
            esc = source[j + 1]
            if esc in _SIMPLE_ESCAPES:
                chars.append(_SIMPLE_ESCAPES[esc])
                j += 2
                jbyte += 2
            elif esc == "u":
                hexpart = source[j + 2:j + 6]
                if len(hexpart) != 4 or any(c not in _HEX for c in hexpart):
                    raise ParseError("invalid unicode escape",
                                     jbyte, jbyte + 2 + len(hexpart))
                chars.append(chr(int(hexpart, 16)))
                j += 6
                jbyte += 6
            else:
                raise ParseError("invalid escape '\\%s'" % esc,
                                 jbyte, jbyte + 1 + len(esc.encode("utf-8")))
        else:
            chars.append(ch)
            j += 1
            jbyte += len(ch.encode("utf-8"))
        if len(chars) > limits.max_string:
            raise ParseError("string too long", byte, jbyte)
    raise ParseError("unterminated string", byte, jbyte)
